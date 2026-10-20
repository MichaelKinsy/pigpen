package rpiv_web_tools_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Twins of the /web-tools command and registration cases of the original's index.test.ts, run against a fake
// host (the original drives the registered handler with a mock context). A title that two describe blocks use
// (SearXNG and Ollama) is one twin with a subtest each.

func cfgFile(t *testing.T, h, contents string) {
	t.Helper()
	dir := filepath.Join(h, ".config", "rpiv-web-tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCommand runs /web-tools with args; each answer is the reply to the next select or input (nil cancels).
func runCommand(r *cmdRig, args string, answers ...any) {
	r.mu.Lock()
	r.script = answers
	r.mu.Unlock()
	r.Host.Command("web-tools", args)
}

func firstNotify(t *testing.T, r *cmdRig) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.notifies) == 0 {
		t.Fatal("no notification")
	}
	return r.notifies[0]["message"].(string)
}

func hasLabel(labels []string, want string) bool { return slices.Contains(labels, want) }

func TestIndexRegistration(t *testing.T) {
	const f = "index"
	tw(t, f, "registers web_search + web_fetch tools", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		if !r.tools["web_search"] || !r.tools["web_fetch"] {
			t.Fatalf("tools: %v", r.tools)
		}
	})
	tw(t, f, "registers /web-tools command", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		if !r.cmds["web-tools"] {
			t.Fatalf("commands: %v", r.cmds)
		}
	})
}

func TestIndexCommand(t *testing.T) {
	const f = "index"
	tw(t, f, "preserves keys for all providers when switching", func(t *testing.T) {
		h := home(t)
		cfgFile(t, h, `{"provider":"brave","apiKeys":{"brave":"brave-key","tavily":"tavily-key","jina":"jina-key","firecrawl":"firecrawl-key"}}`)
		r := startRig(t, HostOptions{})
		runCommand(r, "", "Firecrawl", "new-firecrawl-key")
		s := saved(t, h)
		keys := s["apiKeys"].(obj)
		if s["provider"] != "firecrawl" || keys["brave"] != "brave-key" || keys["tavily"] != "tavily-key" || keys["jina"] != "jina-key" || keys["firecrawl"] != "new-firecrawl-key" {
			t.Fatalf("%v", s)
		}
	})
	tw(t, f, "--show displays all providers with masked keys", func(t *testing.T) {
		h := home(t)
		t.Setenv("BRAVE_SEARCH_API_KEY", "sk-live-abcdefghijklmnop")
		cfgFile(t, h, `{"provider":"brave","apiKeys":{"brave":"sk-cfg-abcdefghijklmnop"}}`)
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		msg := firstNotify(t, r)
		for _, want := range []string{"sk-l...mnop", "sk-c...mnop", "active provider: brave"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("%q lacks %q", msg, want)
			}
		}
	})
	tw(t, f, "--show shows '(not set)' when nothing configured", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		if !strings.Contains(firstNotify(t, r), "(not set)") {
			t.Fatal(firstNotify(t, r))
		}
	})
	tw(t, f, "input cancelled after select leaves config untouched", func(t *testing.T) {
		h := home(t)
		cfgFile(t, h, `{"apiKey":"existing"}`)
		r := startRig(t, HostOptions{})
		runCommand(r, "", "Serper", nil)
		if saved(t, h)["apiKey"] != "existing" {
			t.Fatalf("%v", saved(t, h))
		}
	})
	tw(t, f, "empty input after select leaves config untouched when no existing key", func(t *testing.T) {
		h := home(t)
		cfgFile(t, h, `{"apiKey":"existing"}`)
		r := startRig(t, HostOptions{})
		// Exa has no key anywhere (the legacy apiKey belongs to Brave), so an empty answer is a cancel.
		runCommand(r, "", "Exa", "   ")
		s := saved(t, h)
		if _, has := s["provider"]; s["apiKey"] != "existing" || has {
			t.Fatalf("%v", s)
		}
	})
	tw(t, f, "marks every provider with a saved key as (configured)", func(t *testing.T) {
		h := home(t)
		cfgFile(t, h, `{"provider":"exa","apiKeys":{"exa":"exa-key","brave":"brave-key","tavily":"tavily-key"}}`)
		r := startRig(t, HostOptions{})
		runCommand(r, "", nil)
		labels := r.selects[0]
		if labels[0] != "Exa ✓ (configured)" {
			t.Fatalf("%v", labels)
		}
		for _, want := range []string{"Brave (configured)", "Tavily (configured)", "Serper", "Jina", "Firecrawl"} {
			if !hasLabel(labels, want) {
				t.Fatalf("%v lacks %q", labels, want)
			}
		}
	})
}

// selfHosted is one of the two self-hosted providers; the original has a describe block for each, with the same
// titles, so every such title is twinned twice (SearXNG first, as upstream orders them).
type selfHosted struct{ label, name string }

var (
	searxngProvider = selfHosted{"SearXNG", "searxng"}
	ollamaProvider  = selfHosted{"Ollama", "ollama"}
)

func promptsURLThenKey(t *testing.T, p selfHosted) {
	h := home(t)
	r := startRig(t, HostOptions{})
	runCommand(r, "", p.label, "http://my-host:1234", "my-bearer")
	s := saved(t, h)
	if s["provider"] != p.name || s["baseUrls"].(obj)[p.name] != "http://my-host:1234" || s["apiKeys"].(obj)[p.name] != "my-bearer" {
		t.Fatalf("%v", s)
	}
	if len(r.inputs) != 2 {
		t.Fatalf("inputs: %v", r.inputs)
	}
	title0, _, _ := strings.Cut(r.inputs[0], "|")
	title1, _, _ := strings.Cut(r.inputs[1], "|")
	if !regexp.MustCompile(`(?i)URL`).MatchString(title0) || !regexp.MustCompile(`(?i)key`).MatchString(title1) {
		t.Fatalf("inputs: %v", r.inputs)
	}
}

func emptyURLFallsBack(t *testing.T, p selfHosted, def string) {
	h := home(t)
	r := startRig(t, HostOptions{})
	runCommand(r, "", p.label, "", "")
	s := saved(t, h)
	keys, _ := s["apiKeys"].(obj)
	if _, has := keys[p.name]; s["provider"] != p.name || s["baseUrls"].(obj)[p.name] != def || has {
		t.Fatalf("%v", s)
	}
}

func urlCancelLeavesConfig(t *testing.T, p selfHosted) {
	h := home(t)
	cfgFile(t, h, `{"provider":"brave","apiKey":"existing"}`)
	r := startRig(t, HostOptions{})
	runCommand(r, "", p.label, nil)
	s := saved(t, h)
	if s["provider"] != "brave" || s["apiKey"] != "existing" {
		t.Fatalf("%v", s)
	}
}

func keepsExistingURLAndKey(t *testing.T, p selfHosted) {
	h := home(t)
	cfgFile(t, h, `{"provider":"`+p.name+`","baseUrls":{"`+p.name+`":"http://existing:8080"},"apiKeys":{"`+p.name+`":"existing-key"}}`)
	r := startRig(t, HostOptions{})
	runCommand(r, "", p.label+" ✓ (configured)", "", "")
	s := saved(t, h)
	if s["baseUrls"].(obj)[p.name] != "http://existing:8080" || s["apiKeys"].(obj)[p.name] != "existing-key" {
		t.Fatalf("%v", s)
	}
}

func TestIndexCommandSelfHosted(t *testing.T) {
	const f = "index"
	tw(t, f, "prompts URL first, then optional key, and persists both", func(t *testing.T) { promptsURLThenKey(t, searxngProvider) })
	tw(t, f, "empty URL input falls back to the default URL and leaves key unset", func(t *testing.T) { emptyURLFallsBack(t, searxngProvider, "http://localhost:8080") })
	tw(t, f, "URL cancel (undefined) leaves config untouched", func(t *testing.T) { urlCancelLeavesConfig(t, searxngProvider) })
	tw(t, f, "keeps existing URL and key when both inputs are empty", func(t *testing.T) { keepsExistingURLAndKey(t, searxngProvider) })
	tw(t, f, "prompts URL first, then optional key, and persists both", func(t *testing.T) { promptsURLThenKey(t, ollamaProvider) })
	tw(t, f, "empty URL input falls back to the default URL and leaves key unset", func(t *testing.T) { emptyURLFallsBack(t, ollamaProvider, "http://localhost:11434") })
	tw(t, f, "URL cancel (undefined) leaves config untouched", func(t *testing.T) { urlCancelLeavesConfig(t, ollamaProvider) })
	tw(t, f, "keeps existing URL and key when both inputs are empty", func(t *testing.T) { keepsExistingURLAndKey(t, ollamaProvider) })
	tw(t, f, "marks searxng (configured) when SEARXNG_URL env is set, but not when only the default applies", func(t *testing.T) {
		// The default URL alone is not "configured": the marker tells the user they set something.
		home(t)
		r := startRig(t, HostOptions{})
		runCommand(r, "", nil)
		if !hasLabel(r.selects[0], "SearXNG") || hasLabel(r.selects[0], "SearXNG (configured)") {
			t.Fatalf("%v", r.selects[0])
		}
		t.Setenv("SEARXNG_URL", "http://my-searx:8080")
		r = startRig(t, HostOptions{})
		runCommand(r, "", nil)
		if !hasLabel(r.selects[0], "SearXNG (configured)") {
			t.Fatalf("%v", r.selects[0])
		}
	})
	showSource := func(t *testing.T, source string) {
		h := home(t)
		want := "http://my-searx:8080"
		switch source {
		case "env":
			t.Setenv("SEARXNG_URL", want)
		case "config":
			want = "http://config-host:7000"
			cfgFile(t, h, `{"baseUrls":{"searxng":"`+want+`"}}`)
		default:
			want = "http://localhost:8080"
		}
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		msg := firstNotify(t, r)
		if !strings.Contains(msg, "searxng url: "+want) || !strings.Contains(msg, "source: "+source) {
			t.Fatal(msg)
		}
	}
	tw(t, f, "--show surfaces the resolved searxng URL and its source (env)", func(t *testing.T) { showSource(t, "env") })
	tw(t, f, "--show surfaces the resolved searxng URL and its source (config)", func(t *testing.T) { showSource(t, "config") })
	tw(t, f, "--show surfaces the resolved searxng URL and its source (default)", func(t *testing.T) { showSource(t, "default") })
}

func TestIndexCommandInterceptorsAndSource(t *testing.T) {
	const f = "index"
	tw(t, f, "--show lists 'github: disabled' with how-to-enable hint when interceptor is off", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		msg := firstNotify(t, r)
		if !strings.Contains(msg, "URL interceptors:") || !strings.Contains(msg, "github: disabled") || !regexp.MustCompile(`enable.*interceptors.*github.*true`).MatchString(msg) {
			t.Fatal(msg)
		}
	})
	tw(t, f, "reports source: env when WEB_SEARCH_PROVIDER is set", func(t *testing.T) {
		home(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		if !strings.Contains(firstNotify(t, r), "active provider: tavily (source: env)") {
			t.Fatal(firstNotify(t, r))
		}
	})
	tw(t, f, "reports source: config when config.provider is set and no env", func(t *testing.T) {
		h := home(t)
		cfgFile(t, h, `{"provider":"brave"}`)
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		if !strings.Contains(firstNotify(t, r), "active provider: brave (source: config)") {
			t.Fatal(firstNotify(t, r))
		}
	})
	tw(t, f, "reports source: default when neither env nor config is set", func(t *testing.T) {
		home(t)
		r := startRig(t, HostOptions{})
		runCommand(r, "--show")
		if !strings.Contains(firstNotify(t, r), "active provider: brave (source: default)") {
			t.Fatal(firstNotify(t, r))
		}
	})
	tw(t, f, "lists the env-named provider first and marks only it ✓ when no config", func(t *testing.T) {
		home(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
		r := startRig(t, HostOptions{})
		runCommand(r, "", nil)
		marked := 0
		for _, l := range r.selects[0] {
			if strings.Contains(l, "✓") {
				marked++
			}
		}
		if r.selects[0][0] != "Tavily ✓" || marked != 1 {
			t.Fatalf("%v", r.selects[0])
		}
	})
}
