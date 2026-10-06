package rpiv_web_tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rpiv_web_tools "github.com/MichaelKinsy/pigpen/rpiv-web-tools"
)

type obj = map[string]any

type cmdRig struct {
	*Host
	mu       sync.Mutex
	script   []any // a string answers, nil cancels
	selects  [][]string
	inputs   []string
	notifies []obj
}

func startRig(t *testing.T, opts HostOptions) *cmdRig {
	t.Helper()
	r := &cmdRig{}
	opts.OnCallValue = func(method string, args map[string]any) (any, string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		next := func() any {
			if len(r.script) == 0 {
				return nil
			}
			a := r.script[0]
			r.script = r.script[1:]
			return a
		}
		switch method {
		case "ui.select":
			var choices []string
			for _, c := range args["options"].([]any) {
				choices = append(choices, c.(string))
			}
			r.selects = append(r.selects, choices)
			if s, ok := next().(string); ok {
				return obj{"selected": s, "ok": true}, ""
			}
			return obj{"ok": false}, ""
		case "ui.input":
			r.inputs = append(r.inputs, args["title"].(string)+"|"+args["placeholder"].(string))
			if s, ok := next().(string); ok {
				return obj{"text": s, "ok": true}, ""
			}
			return obj{"ok": false}, ""
		case "ui.notify":
			r.notifies = append(r.notifies, args)
		}
		return obj{}, ""
	}
	r.Host = StartHost(t, rpiv_web_tools.Extension(), opts)
	return r
}

func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", "")
	os.Unsetenv("XDG_CONFIG_HOME")
	for _, k := range []string{"WEB_SEARCH_PROVIDER", "BRAVE_SEARCH_API_KEY", "TAVILY_API_KEY", "SERPER_API_KEY", "EXA_API_KEY", "YOUCOM_API_KEY", "JINA_API_KEY",
		"FIRECRAWL_API_KEY", "PERPLEXITY_API_KEY", "SEARXNG_API_KEY", "SEARXNG_URL", "OLLAMA_API_KEY", "OLLAMA_HOST"} {
		os.Unsetenv(k)
	}
	return h
}

func saved(t *testing.T, h string) obj {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h, ".config", "rpiv-web-tools", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m obj
	json.Unmarshal(data, &m)
	return m
}

func lastNotify(r *cmdRig) (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.notifies[len(r.notifies)-1]
	return n["message"].(string), n["level"].(string)
}

func TestWebToolsCommand(t *testing.T) {
	const f = "index"
	run := func(r *cmdRig, args string, answers ...any) string {
		r.mu.Lock()
		r.script = answers
		r.mu.Unlock()
		return r.Host.Command("web-tools", args)
	}
	tw(t, f, "!hasUI notifies error", func(t *testing.T) {
		home(t)
		no := false
		r := startRig(t, HostOptions{Mode: "print", HasUI: &no})
		run(r, "")
		msg, level := lastNotify(r)
		if msg != "/web-tools requires interactive mode" || level != "error" {
			t.Fatalf("%q %q", msg, level)
		}
	})
	tw(t, f, "two-step: select provider then enter key", func(t *testing.T) {
		h := home(t)
		r := startRig(t, HostOptions{})
		run(r, "", "Tavily", "tvly-new")
		s := saved(t, h)
		if s["provider"] != "tavily" || s["apiKeys"].(obj)["tavily"] != "tvly-new" {
			t.Fatalf("%v", s)
		}
		if _, has := s["apiKey"]; has {
			t.Fatal("the legacy apiKey survived")
		}
		msg, _ := lastNotify(r)
		if !strings.HasPrefix(msg, "Saved Tavily API key to ") {
			t.Fatal(msg)
		}
	})
	tw(t, f, "select cancelled leaves config untouched", func(t *testing.T) {
		h := home(t)
		r := startRig(t, HostOptions{})
		run(r, "", nil)
		if _, err := os.Stat(filepath.Join(h, ".config", "rpiv-web-tools", "config.json")); err == nil {
			t.Fatal("a config was written")
		}
		msg, level := lastNotify(r)
		if msg != "Web search config unchanged" || level != "info" {
			t.Fatal(msg)
		}
	})
	tw(t, f, "empty input keeps existing key and persists provider switch", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Join(h, ".config", "rpiv-web-tools"), 0o755)
		os.WriteFile(filepath.Join(h, ".config", "rpiv-web-tools", "config.json"), []byte(`{"provider":"brave","apiKeys":{"serper":"serper-key-0001"}}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "Serper (configured)", "")
		s := saved(t, h)
		if s["provider"] != "serper" || s["apiKeys"].(obj)["serper"] != "serper-key-0001" {
			t.Fatalf("%v", s)
		}
		msg, _ := lastNotify(r)
		if msg != "Active provider set to Serper; existing key kept" {
			t.Fatal(msg)
		}
		if r.inputs[0] != "Serper API key|Press Enter to keep current (serp...0001), or type new key" {
			t.Fatal(r.inputs[0])
		}
	})
	tw(t, f, "migrates legacy apiKey to apiKeys on save", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Join(h, ".config", "rpiv-web-tools"), 0o755)
		os.WriteFile(filepath.Join(h, ".config", "rpiv-web-tools", "config.json"), []byte(`{"apiKey":"legacy-key","otherField":"keep"}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "Brave (configured) ✓", "")
		s := saved(t, h)
		if s["apiKeys"].(obj)["brave"] != "legacy-key" || s["otherField"] != "keep" {
			t.Fatalf("%v", s)
		}
		if _, has := s["apiKey"]; has {
			t.Fatal("the legacy apiKey survived")
		}
	})
	tw(t, "web-tools.guidance", "preserves guidance when saving API key via /web-tools", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Join(h, ".config", "rpiv-web-tools"), 0o755)
		os.WriteFile(filepath.Join(h, ".config", "rpiv-web-tools", "config.json"), []byte(`{"guidance":{"web_search":{"promptSnippet":"Custom"}}}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "Brave", "new-api-key")
		s := saved(t, h)
		g, _ := json.Marshal(s["guidance"])
		if s["provider"] != "brave" || s["apiKeys"].(obj)["brave"] != "new-api-key" || string(g) != `{"web_search":{"promptSnippet":"Custom"}}` {
			t.Fatalf("%v", s)
		}
		if _, has := s["apiKey"]; has {
			t.Fatal("the legacy apiKey survived")
		}
	})
	tw(t, f, "lists active provider first with a ✓ marker", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Join(h, ".config", "rpiv-web-tools"), 0o755)
		os.WriteFile(filepath.Join(h, ".config", "rpiv-web-tools", "config.json"), []byte(`{"provider":"exa","apiKeys":{"exa":"k","tavily":"t"}}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", nil)
		c := r.selects[0]
		if c[0] != "Exa ✓ (configured)" || c[1] != "Brave" || c[2] != "Tavily (configured)" {
			t.Fatalf("%v", c)
		}
	})
	tw(t, f, "defaults to brave-first when no provider is configured", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		run(r, "", nil)
		if r.selects[0][0] != "Brave ✓" || len(r.selects[0]) != 10 {
			t.Fatalf("%v", r.selects[0])
		}
	})
	tw(t, f, "marks provider as (configured) when key is in env var", func(t *testing.T) {
		home(t)
		t.Setenv("SERPER_API_KEY", "env-key")
		r := startRig(t, HostOptions{})
		run(r, "", nil)
		found := false
		for _, c := range r.selects[0] {
			if c == "Serper (configured)" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%v", r.selects[0])
		}
	})
	tw(t, f, "notifies error and skips 'Saved …' when the underlying write fails", func(t *testing.T) {
		h := home(t)
		// The config directory cannot be created: a file stands where it must be.
		os.WriteFile(filepath.Join(h, ".config"), []byte("x"), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "Brave", "key-1")
		msg, level := lastNotify(r)
		if level != "error" || !strings.HasPrefix(msg, "Failed to save Brave API key to ") || !strings.HasSuffix(msg, " — disk write failed") {
			t.Fatal(msg, level)
		}
	})
}

func TestWebToolsCommandExtra(t *testing.T) {
	run := func(r *cmdRig, args string, answers ...any) {
		r.mu.Lock()
		r.script = answers
		r.mu.Unlock()
		r.Host.Command("web-tools", args)
	}
	cfgPath := func(h string) string { return filepath.Join(h, ".config", "rpiv-web-tools", "config.json") }
	t.Run("--show is found anywhere in the arguments", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		run(r, "  --show  ")
		msg, _ := lastNotify(r)
		if !strings.HasPrefix(msg, "Web search config:") {
			t.Fatal(msg)
		}
		if len(r.selects) != 0 {
			t.Fatal("the picker opened")
		}
	})
	t.Run("SearXNG with empty answers saves the default URL and no key", func(t *testing.T) {
		h := home(t)
		r := startRig(t, HostOptions{})
		run(r, "", "SearXNG", "", "")
		s := saved(t, h)
		if s["provider"] != "searxng" || s["baseUrls"].(obj)["searxng"] != "http://localhost:8080" {
			t.Fatalf("%v", s)
		}
		if _, has := s["apiKeys"]; has {
			t.Fatalf("a key was saved: %v", s)
		}
		msg, _ := lastNotify(r)
		if !strings.HasPrefix(msg, "Saved SearXNG config (url: http://localhost:8080) to ") {
			t.Fatal(msg)
		}
	})
	t.Run("a provider with its own flow drops the legacy key as well", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Dir(cfgPath(h)), 0o755)
		os.WriteFile(cfgPath(h), []byte(`{"apiKey":"legacy","provider":"brave"}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "Ollama", "http://ollama.lan:11434", "okey-1234")
		s := saved(t, h)
		if _, has := s["apiKey"]; has {
			t.Fatalf("the legacy key survived: %v", s)
		}
		if s["apiKeys"].(obj)["ollama"] != "okey-1234" || s["baseUrls"].(obj)["ollama"] != "http://ollama.lan:11434" {
			t.Fatalf("%v", s)
		}
	})
	t.Run("the optional key prompt masks the key it keeps", func(t *testing.T) {
		h := home(t)
		os.MkdirAll(filepath.Dir(cfgPath(h)), 0o755)
		os.WriteFile(cfgPath(h), []byte(`{"apiKeys":{"searxng":"searx-secret-0001"}}`), 0o644)
		r := startRig(t, HostOptions{})
		run(r, "", "SearXNG", "", "")
		want := "SearXNG API key (optional — for instances behind a Bearer-auth proxy)|Press Enter to keep current (sear...0001), or type new key"
		if len(r.inputs) < 2 || r.inputs[1] != want {
			t.Fatalf("%q", r.inputs)
		}
		if saved(t, h)["apiKeys"].(obj)["searxng"] != "searx-secret-0001" {
			t.Fatal("the kept key was lost")
		}
	})
}
