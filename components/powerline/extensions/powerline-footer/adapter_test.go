package powerline_footer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The SDK adapter (extension.go) in an interactive host. The Pi-recorded scenarios run in RPC mode, where the bar draws nothing,
// so these tests are the only check of what the adapter reads from the host and draws.

type tuiRig struct {
	settings    string         // settings.json under the agent dir ("" for none)
	themeJSON   string         // extensions/powerline-footer/theme.json ("" for none)
	sessionName *string        // nil: the host fails getSessionName
	oauth       bool           // the model's provider signs in with OAuth
	state       map[string]any // a state_update sent before session_start (settings, theme)
}

func (r tuiRig) start(t *testing.T) *Host {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	if r.settings != "" {
		os.MkdirAll(agent, 0o755)
		os.WriteFile(filepath.Join(agent, "settings.json"), []byte(r.settings), 0o644)
	}
	if r.themeJSON != "" {
		dir := filepath.Join(agent, "extensions", "powerline-footer")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "theme.json"), []byte(r.themeJSON), 0o644)
	}
	h := StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getModelInfo":
			return map[string]any{"id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5", "provider": "anthropic", "reasoning": true, "contextWindow": 200000}, ""
		case "getContextUsage":
			return nil, "no usage yet" // unknown: the bar falls back to the model's window
		case "getSessionID":
			return map[string]any{"sessionId": "0123456789abcdef"}, ""
		case "getSessionName":
			if r.sessionName == nil {
				return nil, "no session"
			}
			return map[string]any{"name": *r.sessionName}, ""
		case "getThinkingLevel":
			return map[string]any{"level": "medium"}, ""
		case "getModelRegistryState":
			return map[string]any{"providers": map[string]any{"anthropic": map[string]any{"usingOAuth": r.oauth}}}, ""
		}
		return map[string]any{}, ""
	}})
	if r.state != nil {
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": r.state}}})
	}
	h.Fire("session_start", map[string]any{"reason": "startup"})
	return h
}

// lastWidget returns the content and options of the last ui.setWidget call for key.
func lastWidget(t *testing.T, h *Host, key string) (string, map[string]any) {
	t.Helper()
	var content []any
	var options map[string]any
	found := false
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] == key {
			content, _ = c.Args["content"].([]any)
			options, _ = c.Args["options"].(map[string]any)
			found = true
		}
	}
	if !found {
		t.Fatalf("no ui.setWidget call for %s", key)
	}
	var lines []string
	for _, l := range content {
		lines = append(lines, l.(string))
	}
	return strings.Join(lines, "\n"), options
}

func TestTUIBarShowsTheModelAndFallsBackToItsContextWindow(t *testing.T) {
	h := tuiRig{}.start(t)
	top, options := lastWidget(t, h, "powerline-top")
	if !strings.Contains(top, "Sonnet 4.5") {
		t.Errorf("top bar %q lacks the model", top)
	}
	if !strings.Contains(top, "0/200k") {
		t.Errorf("top bar %q lacks the model's context window (unknown usage falls back to it)", top)
	}
	if options["placement"] != "aboveEditor" {
		t.Errorf("placement %v, want aboveEditor", options["placement"])
	}
	if len(h.CallsTo("ui.setFooter")) == 0 {
		t.Error("the footer renderer was not installed")
	}
}

func TestTUIPlacementBelowFromTheSettings(t *testing.T) {
	h := tuiRig{settings: `{"powerline":{"placement":"below"}}`}.start(t)
	if _, options := lastWidget(t, h, "powerline-top"); options["placement"] != "belowEditor" {
		t.Errorf("placement %v, want belowEditor", options["placement"])
	}
}

func TestTUIAutoCompactFollowsTheHostSettings(t *testing.T) {
	on, _ := lastWidget(t, tuiRig{}.start(t), "powerline-top")
	if !strings.Contains(on, " AC") {
		t.Errorf("auto-compaction is on unless the settings turn it off; bar %q lacks AC", on)
	}
	unset, _ := lastWidget(t, tuiRig{state: map[string]any{"settings": map[string]any{"compaction": map[string]any{"reserveTokens": 1}}}}.start(t), "powerline-top")
	if !strings.Contains(unset, " AC") {
		t.Errorf("settings without compaction.enabled keep auto-compaction on; bar %q lacks AC", unset)
	}
	off, _ := lastWidget(t, tuiRig{state: map[string]any{"settings": map[string]any{"compaction": map[string]any{"enabled": false}}}}.start(t), "powerline-top")
	if strings.Contains(off, " AC") {
		t.Errorf("compaction.enabled=false: bar %q still shows AC", off)
	}
}

func TestTUISubscriptionComesFromTheModelsProvider(t *testing.T) {
	sub, _ := lastWidget(t, tuiRig{oauth: true}.start(t), "powerline-top")
	if !strings.Contains(sub, "(sub)") {
		t.Errorf("an OAuth provider shows (sub); bar %q", sub)
	}
	paid, _ := lastWidget(t, tuiRig{}.start(t), "powerline-top")
	if strings.Contains(paid, "(sub)") {
		t.Errorf("an API-key provider shows no (sub); bar %q", paid)
	}
}

func TestTUISessionNameOrTheSessionID(t *testing.T) {
	layout := `{"powerline":{"layout":{"left":["model","session"]}}}`
	name := "release-work"
	named, _ := lastWidget(t, tuiRig{settings: layout, sessionName: &name}.start(t), "powerline-top")
	if !strings.Contains(named, "release-work") {
		t.Errorf("bar %q lacks the session name", named)
	}
	unnamed, _ := lastWidget(t, tuiRig{settings: layout}.start(t), "powerline-top")
	if !strings.Contains(unnamed, "01234567") || strings.Contains(unnamed, "release-work") {
		t.Errorf("without a name the bar shows the session id; bar %q", unnamed)
	}
}

// Pi 1.0.0's Theme.fg knows scrollbarThumb (theme.d.ts ThemeColor), so a theme.json color naming it is drawn with it, not with
// the "text" fallback an unknown token gets.
func TestTUIThemeTokensArePi100s(t *testing.T) {
	theme := map[string]any{"foregrounds": map[string]any{"scrollbarThumb": "\x1b[38;5;99m", "text": "\x1b[38;5;7m", "accent": "\x1b[38;5;1m"}}
	h := tuiRig{themeJSON: `{"colors":{"model":"scrollbarThumb","path":"nonsense"}}`, state: map[string]any{"theme": theme}}.start(t)
	top, _ := lastWidget(t, h, "powerline-top")
	if !strings.Contains(top, "\x1b[38;5;99mSonnet 4.5") {
		t.Errorf("model not drawn with scrollbarThumb: %q", top)
	}
	if !strings.Contains(top, "\x1b[38;5;7mdir ") {
		t.Errorf("an unknown token falls back to text: %q", top)
	}
}
