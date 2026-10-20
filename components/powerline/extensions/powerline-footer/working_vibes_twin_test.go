package powerline_footer

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of port/oracle/tests/working-vibes.test.ts. The original drives working-vibes.ts directly with a stub context and pi-ai's
// faux provider; here the manager runs against a scripted vibeRegistry, the Go counterpart of that stub (the registry's
// find/getApiKeyAndHeaders/getProvider/getProviderAuth and the faux provider's stream collapse to the four methods of
// vibeRegistry). HOME is a temporary directory, as in the original, so the agent dir is <home>/.pi/agent.

// scriptedRegistry is a vibeRegistry whose answers a test sets.
type scriptedRegistry struct {
	find     func(provider, modelID string) (map[string]any, error)
	auth     func(model map[string]any) (map[string]any, error)
	baseURL  func(provider string) (string, error)
	complete func(model, request, options map[string]any) map[string]any
}

func (r scriptedRegistry) Find(provider, modelID string) (map[string]any, error) {
	if r.find == nil {
		return nil, nil
	}
	return r.find(provider, modelID)
}

func (r scriptedRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	if r.auth == nil {
		return map[string]any{"ok": true, "apiKey": "test-key", "headers": map[string]any{}}, nil
	}
	return r.auth(model)
}

func (r scriptedRegistry) GetProviderBaseURL(provider string) (string, error) {
	if r.baseURL == nil {
		return "", nil
	}
	return r.baseURL(provider)
}

func (r scriptedRegistry) Complete(model, request, options map[string]any) map[string]any {
	return r.complete(model, request, options)
}

// vibeText is faux's fauxAssistantMessage(text).
func vibeText(text string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop"}
}

// testProvider is registration.getModel(...) of a faux provider "test-provider" with the given model ids.
func testProvider(ids ...string) func(provider, modelID string) (map[string]any, error) {
	return func(provider, modelID string) (map[string]any, error) {
		if provider == "test-provider" && slices.Contains(ids, modelID) {
			return map[string]any{"provider": provider, "id": modelID, "api": "faux"}, nil
		}
		return nil, nil
	}
}

// vibeHome points HOME at a fresh directory (no PI_CODING_AGENT_DIR) and returns it with a fresh manager.
func vibeHome(t *testing.T) (string, *vibeManager) {
	t.Helper()
	home, _ := pathEnv(t)
	return home, newVibeManager()
}

// vibeRecorder collects working messages from any goroutine.
type vibeRecorder struct {
	mu   sync.Mutex
	list []string
}

func (r *vibeRecorder) set(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, message)
}

func (r *vibeRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

// bracketTheme is the original test's theme: fg(color, text) = <color>text</color>.
type bracketTheme struct{}

func (bracketTheme) Fg(token, text string) (string, error) {
	return "<" + token + ">" + text + "</" + token + ">", nil
}

func TestWorkingVibes(t *testing.T) {
	tw(t, "working-vibes", "parseVibeGenerateArgs supports multi-word themes", func(t *testing.T) {
		type parsed struct {
			theme string
			count int
			ok    bool
		}
		parse := func(args ...string) parsed {
			theme, count, ok := parseVibeGenerateArgs(args)
			return parsed{theme, count, ok}
		}
		eq(t, parse("pirate", "200"), parsed{"pirate", 200, true}, "pirate 200")
		eq(t, parse("star", "trek", "200"), parsed{"star trek", 200, true}, "star trek 200")
		eq(t, parse("star", "trek"), parsed{"star trek", 100, true}, "star trek")
		eq(t, parse("star", "trek", "abc"), parsed{"star trek abc", 100, true}, "star trek abc")
		eq(t, parse("lord", "of", "rings", "999"), parsed{"lord of rings", 500, true}, "lord of rings 999")
		eq(t, parse(), parsed{}, "no args")
	})

	tw(t, "working-vibes", "working-vibe color styles semantic, hex, and rainbow messages", func(t *testing.T) {
		_, m := vibeHome(t)
		m.initVibeManager(scriptedRegistry{})
		m.setWorkingMessageTheme(bracketTheme{})
		m.setTheme(new("star trek"))
		m.setMode(vibeModeFile)
		m.onAgentStart()

		first := func(color string) string {
			var rec vibeRecorder
			m.setWorkingMessageColor(color)
			m.onBeforeAgentStart("fix a bug", rec.set)
			return rec.all()[0]
		}
		eq(t, first("warning"), "<warning>Channeling star trek...</warning>", "semantic")
		eq(t, first("#89d281"), "\x1b[38;2;137;210;129mChanneling star trek...\x1b[0m", "hex")
		eq(t, first("rainbow"), rainbow("Channeling star trek..."), "rainbow")
		eq(t, first(""), "Channeling star trek...", "default")
		m.onAgentEnd(func(string) {})
	})

	tw(t, "working-vibes", "file vibes accept ellipsis suffixes, reject malformed entries, and use script-appropriate ellipses", func(t *testing.T) {
		home, m := vibeHome(t)
		dir := filepath.Join(home, ".pi", "agent", "vibes")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		lines := []string{"Still working...", "扩展汉字𠀀...", "ひらがな...", "ｶﾀｶﾅ...", "한글...", "注音ㄅ...", "Missing suffix", "...", "……"}
		if err := os.WriteFile(filepath.Join(dir, "scripts.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}

		m.initVibeManager(scriptedRegistry{})
		m.setTheme(new("scripts"))
		m.setMode(vibeModeFile)
		eq(t, m.vibeFileCount("scripts"), 6, "file count")

		var rec vibeRecorder
		for range 6 {
			m.onBeforeAgentStart("test scripts", func(message string) {
				if message != "" {
					rec.set(message)
				}
			})
		}
		got := map[string]bool{}
		for _, message := range rec.all() {
			got[message] = true
		}
		want := map[string]bool{}
		for _, message := range []string{"Channeling scripts...", "Still working...", "扩展汉字𠀀……", "ひらがな……", "ｶﾀｶﾅ……", "한글……", "注音ㄅ……"} {
			want[message] = true
		}
		eq(t, got, want, "messages")
		m.onAgentEnd(func(string) {})
	})

	tw(t, "working-vibes", "generateVibesBatch includes a system prompt so faux providers can return text", func(t *testing.T) {
		_, m := vibeHome(t)
		m.initVibeManager(scriptedRegistry{
			find: testProvider("test-model"),
			complete: func(_, request, _ map[string]any) map[string]any {
				if !regexp.MustCompile(`(?i)loading messages`).MatchString(request["systemPrompt"].(string)) {
					t.Errorf("system prompt %q does not mention loading messages", request["systemPrompt"])
				}
				return vibeText("Engaging warp drive...\nRunning diagnostics...")
			},
		})
		eq(t, m.setModel("test-provider/test-model"), true, "setVibeModel")

		result, err := m.generateVibesBatch("star trek", 2)
		eq(t, err, nil, "error")
		eq(t, result.Success, true, "success")
		eq(t, result.Count, 2, "count")
		data, readErr := os.ReadFile(result.FilePath)
		eq(t, readErr, nil, "file exists")
		eq(t, strings.Split(strings.TrimSpace(string(data)), "\n"), []string{"Engaging warp drive...", "Running diagnostics..."}, "file")
	})

	tw(t, "working-vibes", "generateVibesBatch forwards resolved provider env and credential base URL", func(t *testing.T) {
		_, m := vibeHome(t)
		m.initVibeManager(scriptedRegistry{
			find: testProvider("test-model"),
			auth: func(map[string]any) (map[string]any, error) {
				return map[string]any{"ok": true, "apiKey": "test-key", "headers": map[string]any{}, "env": map[string]any{"AWS_PROFILE": "vibes"}}, nil
			},
			baseURL: func(string) (string, error) { return "https://credential.example/v1", nil },
			complete: func(model, _, options map[string]any) map[string]any {
				eq(t, options["env"], map[string]any{"AWS_PROFILE": "vibes"}, "env")
				eq(t, model["baseUrl"], "https://credential.example/v1", "base URL")
				return vibeText("Signing the request...")
			},
		})
		eq(t, m.setModel("test-provider/test-model"), true, "setVibeModel")

		result, _ := m.generateVibesBatch("bedrock", 1)
		eq(t, result.Success, true, "success")
		data, _ := os.ReadFile(result.FilePath)
		eq(t, strings.Split(strings.TrimSpace(string(data)), "\n"), []string{"Signing the request..."}, "file")
	})

	tw(t, "working-vibes", "generateVibesBatch parses thinking suffix separately from model id", func(t *testing.T) {
		_, m := vibeHome(t)
		var reasoning []any
		replies := []string{"Quietly calculating...", "Literal model selected..."}
		m.initVibeManager(scriptedRegistry{
			find: testProvider("test-model", "literal-model:low"),
			complete: func(_, _, options map[string]any) map[string]any {
				reasoning = append(reasoning, options["reasoning"])
				reply := replies[0]
				replies = replies[1:]
				return vibeText(reply)
			},
		})
		eq(t, m.setModel("test-provider/test-model:low"), true, "setVibeModel suffix")

		result, _ := m.generateVibesBatch("math", 1)
		eq(t, result.Success, true, "success")
		data, _ := os.ReadFile(result.FilePath)
		eq(t, strings.Split(strings.TrimSpace(string(data)), "\n"), []string{"Quietly calculating..."}, "file")

		eq(t, m.setModel("test-provider/literal-model:low"), true, "setVibeModel literal")
		literal, _ := m.generateVibesBatch("math", 1)
		eq(t, literal.Success, true, "literal success")
		data, _ = os.ReadFile(literal.FilePath)
		eq(t, strings.Split(strings.TrimSpace(string(data)), "\n"), []string{"Literal model selected..."}, "literal file")
		eq(t, reasoning, []any{"low", nil}, "reasoning options")
	})

	tw(t, "working-vibes", "on-demand vibe generation includes a system prompt for providers that require instructions", func(t *testing.T) {
		_, m := vibeHome(t)
		m.initVibeManager(scriptedRegistry{
			find: testProvider("test-model"),
			complete: func(_, request, _ map[string]any) map[string]any {
				if !regexp.MustCompile(`(?i)loading messages`).MatchString(request["systemPrompt"].(string)) {
					t.Errorf("system prompt %q does not mention loading messages", request["systemPrompt"])
				}
				return vibeText("Engaging warp drive...")
			},
		})
		eq(t, m.setTheme(new("star trek")), true, "setVibeTheme")
		eq(t, m.setModel("test-provider/test-model"), true, "setVibeModel")

		var rec vibeRecorder
		m.onAgentStart()
		m.onBeforeAgentStart("fix a bug", rec.set)

		deadline := time.Now().Add(time.Second)
		for !slices.Contains(rec.all(), "Engaging warp drive...") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		updates := rec.all()
		eq(t, updates[0], "Channeling star trek...", "placeholder first")
		eq(t, slices.Contains(updates, "Engaging warp drive..."), true, "generated vibe")
	})

	tw(t, "working-vibes", "on-demand vibe generation silently ignores stale contexts but logs unrelated failures", func(t *testing.T) {
		_, m := vibeHome(t)
		eq(t, m.setTheme(new("star trek")), true, "setVibeTheme")
		eq(t, m.setMode(vibeModeGenerate), true, "setVibeMode")
		eq(t, m.setModel("test-provider/test-model"), true, "setVibeModel")

		runFailure := func(failure error) (logs [][]any, updates []string) {
			var rec vibeRecorder
			var mu sync.Mutex
			m.debug = func(args ...any) {
				mu.Lock()
				defer mu.Unlock()
				logs = append(logs, args)
			}
			m.initVibeManager(scriptedRegistry{find: func(string, string) (map[string]any, error) { return nil, failure }})
			m.onAgentStart()
			m.onBeforeAgentStart("fix a bug", rec.set)
			m.wait()
			m.onAgentEnd(func(string) {})
			mu.Lock()
			defer mu.Unlock()
			return logs, rec.all()
		}

		logs, updates := runFailure(errors.New("This extension ctx is stale after session replacement or reload."))
		eq(t, updates, []string{"Channeling star trek..."}, "stale updates")
		eq(t, len(logs), 0, "stale logs")

		unrelated := errors.New("registry unavailable")
		logs, updates = runFailure(unrelated)
		eq(t, updates, []string{"Channeling star trek..."}, "unrelated updates")
		eq(t, logs, [][]any{{"[working-vibes] Generation failed:", unrelated}}, "unrelated logs")
	})

	tw(t, "working-vibes", "generateVibesBatch preserves provider errors instead of reporting an empty response", func(t *testing.T) {
		_, m := vibeHome(t)
		m.initVibeManager(scriptedRegistry{
			find: testProvider("test-model"),
			complete: func(_, _, _ map[string]any) map[string]any {
				return map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "Instructions are required"}
			},
		})
		eq(t, m.setModel("test-provider/test-model"), true, "setVibeModel")

		result, _ := m.generateVibesBatch("noir", 2)
		eq(t, result.Success, false, "success")
		eq(t, result.Error, "Instructions are required", "error")
	})
}
