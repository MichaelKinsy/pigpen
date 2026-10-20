package powerline_footer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The working vibes and /vibe through the SDK adapter (working_vibes_ext.go) in an interactive host. The Pi-recorded scenarios
// run in RPC mode, where Pi's setWorkingMessage is a no-op, so the working messages are checked here.

type vibeRig struct {
	settings string // settings.json under the agent dir
	hasUI    *bool
	mode     string
	branch   []any // the session branch getBranch answers
	// reply answers each model request (the extension's own part of the turn): the text of the assistant message.
	reply func(request map[string]any) string
	state map[string]any // a state_update sent before session_start (the theme)
}

type vibeHost struct {
	*Host
	agent string
	mu    sync.Mutex
	reqs  []map[string]any // modelStream args, in order
}

func (r vibeRig) start(t *testing.T) *vibeHost {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	if r.settings != "" {
		_ = os.MkdirAll(agent, 0o755)
		_ = os.WriteFile(filepath.Join(agent, "settings.json"), []byte(r.settings), 0o644)
	}
	vh := &vibeHost{agent: agent}
	reply := r.reply
	if reply == nil {
		reply = func(map[string]any) string { return "Engaging warp drive..." }
	}
	vh.Host = StartHost(t, powerline.Extension(), HostOptions{
		Mode:  r.mode,
		HasUI: r.hasUI,
		OnCallValue: func(method string, args map[string]any) (any, string) {
			switch method {
			case "getModel":
				if args["provider"] == "test-provider" && (args["modelId"] == "test-model" || args["modelId"] == "other-model") {
					return map[string]any{"provider": "test-provider", "id": args["modelId"], "api": "faux"}, ""
				}
				return json.RawMessage("null"), "" // a nil answer would reach the SDK as {}
			case "getModelAuth":
				return map[string]any{"ok": true, "apiKey": "test-key", "headers": map[string]any{"x-test": "1"}}, ""
			case "getProviderAuth":
				return map[string]any{"auth": map[string]any{"baseUrl": "https://credential.example/v1"}}, ""
			case "sessionRead":
				if args["method"] == "getBranch" {
					if r.branch == nil {
						return []any{}, ""
					}
					return r.branch, ""
				}
				return nil, "no session"
			case "getModelInfo":
				return map[string]any{"id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5", "provider": "anthropic", "reasoning": true, "contextWindow": 200000}, ""
			case "getContextUsage", "getSessionName":
				return nil, "none"
			case "getSessionID":
				return map[string]any{"sessionId": "0123456789abcdef"}, ""
			case "getThinkingLevel":
				return map[string]any{"level": "medium"}, ""
			case "getModelRegistryState":
				return map[string]any{"providers": map[string]any{}}, ""
			}
			return map[string]any{}, ""
		},
		ModelStream: func(model, request map[string]any) []map[string]any {
			vh.mu.Lock()
			vh.reqs = append(vh.reqs, map[string]any{"model": model, "request": request})
			vh.mu.Unlock()
			return ModelText(reply(request))
		},
	})
	if r.state != nil {
		vh.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": r.state}}})
	}
	vh.Fire("session_start", map[string]any{"reason": "startup"})
	return vh
}

func (v *vibeHost) requests() []map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.reqs)
}

// workingMessages are the messages of every ui.setWorkingMessage call so far, in order.
func workingMessages(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.setWorkingMessage") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}

// waitFor polls until cond holds (the generation runs in the background, as the original's promise does).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// beforeStart is a before_agent_start event: the SDK requires the system prompt and its options.
func beforeStart(prompt string) map[string]any {
	return map[string]any{"prompt": prompt, "systemPrompt": "", "systemPromptOptions": map[string]any{}}
}

func userText(request map[string]any) string {
	messages, _ := request["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	content, _ := first["content"].([]any)
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

const vibeSettings = `{"workingVibe":"star trek","workingVibeModel":"test-provider/test-model","workingVibeRefreshInterval":0,"powerline":{"workingVibes":{"color":"rainbow"}}}`

func TestVibeIsNoLongerRefused(t *testing.T) {
	h := vibeRig{settings: vibeSettings}.start(t)
	for _, n := range notifications(h.Host) {
		if strings.Contains(n, "does not support") {
			t.Errorf("unexpected refusal: %s", n)
		}
	}
	if failure := h.Command("vibe", ""); failure != "" {
		t.Fatalf("/vibe failed: %s", failure)
	}
}

func TestVibeTurnShowsThePlaceholderThenTheGeneratedMessage(t *testing.T) {
	h := vibeRig{settings: vibeSettings}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("fix a bug"))
	waitFor(t, "the generated vibe", func() bool { return len(workingMessages(h.Host)) >= 2 })

	want := []string{"Channeling star trek...", "Engaging warp drive..."}
	got := workingMessages(h.Host)
	for i, message := range want {
		// workingVibes.color rainbow: each message is drawn with the gradient.
		if got[i] != rainbowOf(message) {
			t.Errorf("working message %d = %q, want the rainbow of %q", i, got[i], message)
		}
	}

	reqs := h.requests()
	if len(reqs) != 1 {
		t.Fatalf("model requests = %d, want 1", len(reqs))
	}
	request := reqs[0]["request"].(map[string]any)
	if request["systemPrompt"] != "You generate short themed loading messages and reply with the requested text only." {
		t.Errorf("system prompt = %q", request["systemPrompt"])
	}
	if text := userText(request); !strings.Contains(text, `"star trek" themed`) || !strings.Contains(text, "Task: fix a bug") {
		t.Errorf("prompt = %q", text)
	}
	if request["apiKey"] != "test-key" {
		t.Errorf("apiKey = %v, want the resolved key", request["apiKey"])
	}
	if model := reqs[0]["model"].(map[string]any); model["baseUrl"] != "https://credential.example/v1" {
		t.Errorf("model baseUrl = %v, want the credential's", model["baseUrl"])
	}

	h.Fire("agent_end", nil)
	if last := workingMessages(h.Host); last[len(last)-1] != "" {
		t.Errorf("agent_end sets %q, want the default message (empty)", last[len(last)-1])
	}
}

func rainbowOf(s string) string { return powerline.Rainbow(s) }

func TestVibeToolCallsRefreshWithAToolHintOrTheAgentText(t *testing.T) {
	branch := []any{
		map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "ignored"}}}},
	}
	h := vibeRig{settings: vibeSettings, branch: branch}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	waitFor(t, "the first request", func() bool { return len(h.requests()) == 1 })

	h.Fire("tool_call", map[string]any{"toolName": "read", "input": map[string]any{"path": "/etc/hosts"}})
	waitFor(t, "the tool-hint request", func() bool { return len(h.requests()) == 2 })
	if text := userText(h.requests()[1]["request"].(map[string]any)); !strings.Contains(text, "Task: reading file: /etc/hosts") {
		t.Errorf("tool hint prompt = %q", text)
	}
}

func TestVibeToolCallUsesTheRecentAssistantText(t *testing.T) {
	branch := []any{
		map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "hmm"},
			map[string]any{"type": "text", "text": "  Compiling the warp core now  "},
		}}},
	}
	h := vibeRig{settings: vibeSettings, branch: branch}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	waitFor(t, "the first request", func() bool { return len(h.requests()) == 1 })
	h.Fire("tool_call", map[string]any{"toolName": "bash", "input": map[string]any{"command": "ls"}})
	waitFor(t, "the second request", func() bool { return len(h.requests()) == 2 })
	if text := userText(h.requests()[1]["request"].(map[string]any)); !strings.Contains(text, "Task: Compiling the warp core now\n") {
		t.Errorf("agent text prompt = %q", text)
	}
}

func TestVibeToolCallRespectsTheRefreshInterval(t *testing.T) {
	settings := strings.Replace(vibeSettings, `"workingVibeRefreshInterval":0`, `"workingVibeRefreshInterval":3600`, 1)
	h := vibeRig{settings: settings}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	waitFor(t, "the first request", func() bool { return len(h.requests()) == 1 })
	h.Fire("tool_call", map[string]any{"toolName": "read", "input": map[string]any{"path": "/x"}})
	time.Sleep(200 * time.Millisecond)
	if n := len(h.requests()); n != 1 {
		t.Errorf("model requests = %d, want 1 (the interval has not passed)", n)
	}
}

func TestVibeDoesNothingWithoutAThemeOrWithoutUI(t *testing.T) {
	no := false
	for name, rig := range map[string]vibeRig{
		"no theme": {settings: `{}`},
		"no UI":    {settings: vibeSettings, hasUI: &no, mode: "json"},
	} {
		t.Run(name, func(t *testing.T) {
			h := rig.start(t)
			h.Fire("agent_start", nil)
			h.Fire("before_agent_start", beforeStart("go"))
			h.Fire("tool_call", map[string]any{"toolName": "read", "input": map[string]any{}})
			h.Fire("agent_end", nil)
			time.Sleep(100 * time.Millisecond)
			// agent_end resets Pi's default message whenever there is a UI, theme or not (onVibeAgentEnd).
			want := []string{""}
			if rig.hasUI != nil {
				want = nil
			}
			if got := workingMessages(h.Host); !slices.Equal(got, want) {
				t.Errorf("working messages = %q, want %q", got, want)
			}
			if n := len(h.requests()); n != 0 {
				t.Errorf("model requests = %d, want 0", n)
			}
		})
	}
}

func TestVibeFileModeNeedsNoModelCall(t *testing.T) {
	h := vibeRig{settings: `{"workingVibe":"pirate","workingVibeMode":"file"}`}.start(t)
	dir := filepath.Join(h.agent, "vibes")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pirate.txt"), []byte("Hoisting the sails...\nSwabbing decks..."), 0o644)
	h.Fire("session_start", map[string]any{"reason": "reload"}) // initVibeManager again: the settings and files are read afresh
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	got := workingMessages(h.Host)
	if len(got) != 2 || got[0] != "Channeling pirate..." || (got[1] != "Hoisting the sails..." && got[1] != "Swabbing decks...") {
		t.Errorf("working messages = %q", got)
	}
	if n := len(h.requests()); n != 0 {
		t.Errorf("file mode made %d model requests", n)
	}
}

func notifyOf(h *Host) []string { return notifications(h) }

func TestVibeCommandNotificationsAndSettings(t *testing.T) {
	h := vibeRig{}.start(t)
	steps := []struct{ args, want string }{
		{"", "info: Vibe: off | Mode: generate | Model: openai-codex/gpt-5.6-luna:low"},
		{"star trek", "info: Vibe set to: star trek"},
		{"", "info: Vibe: star trek | Mode: generate | Model: openai-codex/gpt-5.6-luna:low"},
		{"model", "info: Current vibe model: openai-codex/gpt-5.6-luna:low"},
		{"model nonsense", "error: Invalid model format. Use: provider/modelId (e.g., openai-codex/gpt-5.4-mini)"},
		{"model test-provider/test-model", "info: Vibe model set to: test-provider/test-model"},
		{"mode", "info: Current vibe mode: generate"},
		{"mode bogus", "error: Invalid mode. Use: generate or file"},
		{"mode file", `error: No vibe file for "star trek". Run /vibe generate star trek first`},
		{"generate", "error: Usage: /vibe generate <theme> [count]"},
		{"off", "info: Vibe disabled"},
	}
	for _, s := range steps {
		before := len(notifyOf(h.Host))
		if failure := h.Command("vibe", s.args); failure != "" {
			t.Fatalf("/vibe %s failed: %s", s.args, failure)
		}
		got := notifyOf(h.Host)
		if len(got) != before+1 || got[len(got)-1] != s.want {
			t.Errorf("/vibe %s: notifications %q, want last %q", s.args, got[before:], s.want)
		}
	}

	data, err := os.ReadFile(filepath.Join(h.agent, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if _, set := settings["workingVibe"]; set {
		t.Errorf("/vibe off leaves workingVibe in %s", data)
	}
	if settings["workingVibeModel"] != "test-provider/test-model" {
		t.Errorf("workingVibeModel = %v in %s", settings["workingVibeModel"], data)
	}
	if !strings.HasSuffix(string(data), "}\n") || !strings.Contains(string(data), "\n  \"workingVibeModel\"") {
		t.Errorf("settings.json is not JSON.stringify(_, null, 2) + newline: %q", data)
	}
}

func TestVibeGenerateWritesTheFileAndReportsIt(t *testing.T) {
	h := vibeRig{
		settings: `{"workingVibeModel":"test-provider/test-model"}`,
		reply:    func(map[string]any) string { return "1. Warping in...\n2. \"Engaging thrusters\"\n\n- Cloaking up" },
	}.start(t)
	if failure := h.Command("vibe", "generate star trek 3"); failure != "" {
		t.Fatalf("/vibe generate failed: %s", failure)
	}
	file := filepath.Join(h.agent, "vibes", "star-trek.txt")
	got := notifyOf(h.Host)
	want := []string{
		`info: Generating 3 vibes for "star trek"...`,
		`info: Generated 3 vibes for "star trek" → ` + file,
	}
	if !slices.Equal(got, want) {
		t.Errorf("notifications = %q, want %q", got, want)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "Warping in...\nEngaging thrusters...\nCloaking up..." {
		t.Errorf("vibe file = %q", data)
	}
	if text := userText(h.requests()[0]["request"].(map[string]any)); !strings.HasPrefix(text, `Generate 3 unique 2-4 word loading messages for a "star trek" theme.`) {
		t.Errorf("batch prompt = %q", text)
	}
}

func TestVibeGenerateReportsAMissingModel(t *testing.T) {
	h := vibeRig{}.start(t) // the default model is not in this host's registry
	if failure := h.Command("vibe", "generate noir"); failure != "" {
		t.Fatalf("/vibe generate failed: %s", failure)
	}
	got := notifyOf(h.Host)
	if want := "error: Failed to generate vibes: Model not found: openai-codex/gpt-5.6-luna:low"; got[len(got)-1] != want {
		t.Errorf("last notification = %q, want %q", got[len(got)-1], want)
	}
}

// vibeTheme gives every Pi token a color and "warning" its own, as the host's theme does.
func vibeTheme() map[string]any {
	fg := map[string]any{}
	for _, token := range powerline.PiThemeTokens {
		fg[token] = "\x1b[38;5;250m"
	}
	fg["warning"] = "\x1b[38;5;214m"
	return map[string]any{"theme": map[string]any{"foregrounds": fg}}
}

func TestVibeColorUsesThePiThemeOnceTheBarHasDrawn(t *testing.T) {
	settings := `{"workingVibe":"star trek","workingVibeMode":"file","powerline":{"workingVibes":{"color":"warning"}}}`
	h := vibeRig{settings: settings, state: vibeTheme()}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	got := workingMessages(h.Host)
	if len(got) == 0 || !strings.HasPrefix(got[0], "\x1b[38;5;214m") || !strings.Contains(got[0], "Channeling star trek...") {
		t.Errorf("working messages = %q, want the first drawn with the warning color", got)
	}
}

func TestVibeColorWaitsForTheBarsFirstRender(t *testing.T) {
	// In RPC mode the bar never draws, so the original has no theme for the message (setVibeWorkingMessageTheme is called from
	// the footer's render) and shows it unstyled.
	settings := `{"workingVibe":"star trek","workingVibeMode":"file","powerline":{"workingVibes":{"color":"warning"}}}`
	h := vibeRig{settings: settings, state: vibeTheme(), mode: "rpc"}.start(t)
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	if got := workingMessages(h.Host); len(got) == 0 || got[0] != "Channeling star trek..." {
		t.Errorf("working messages = %q, want the first unstyled", got)
	}
}

func TestVibeToolCallAsksForTheBranchOnlyWhenARefreshIsDue(t *testing.T) {
	settings := strings.Replace(vibeSettings, `"workingVibeRefreshInterval":0`, `"workingVibeRefreshInterval":3600`, 1)
	h := vibeRig{settings: settings}.start(t)
	branchReads := func() int {
		n := 0
		for _, c := range h.CallsTo("sessionRead") {
			if c.Args["method"] == "getBranch" {
				n++
			}
		}
		return n
	}
	h.Fire("tool_call", map[string]any{"toolName": "read", "input": map[string]any{"path": "/x"}}) // not streaming
	h.Fire("agent_start", nil)
	h.Fire("before_agent_start", beforeStart("go"))
	waitFor(t, "the first request", func() bool { return len(h.requests()) == 1 })
	before := branchReads()
	h.Fire("tool_call", map[string]any{"toolName": "read", "input": map[string]any{"path": "/x"}}) // inside the interval
	if got := branchReads(); got != before {
		t.Errorf("a tool call that cannot refresh read the branch (%d reads, was %d)", got, before)
	}
}
