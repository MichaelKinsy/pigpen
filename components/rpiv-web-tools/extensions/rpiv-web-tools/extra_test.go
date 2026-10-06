package rpiv_web_tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cases the original's tests do not carry, each pinning a branch a mutation of this port showed unchecked
// (port/mutations.json), or a behavior of the Go implementation.

func TestKeyAndURLResolution(t *testing.T) {
	t.Run("the environment key wins over the config key", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(tavilyKeyEnv, "env-key")
		k, ok := resolveProviderAPIKey("tavily", webToolsConfig{"apiKeys": obj{"tavily": "config-key"}})
		eq(t, [2]any{k, ok}, [2]any{"env-key", true})
	})
	t.Run("the legacy top-level key belongs to brave alone", func(t *testing.T) {
		clearEnv(t)
		_, ok := resolveProviderAPIKey("tavily", webToolsConfig{"apiKey": "legacy"})
		eq(t, ok, false)
	})
	t.Run("a base URL from the environment wins over the config", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(searxngURLEnv, "http://env.example")
		eq(t, resolveProviderBaseURL(metaOf("searxng"), webToolsConfig{"baseUrls": obj{"searxng": "http://config.example"}}), "http://env.example")
		os.Unsetenv(searxngURLEnv)
		eq(t, resolveProviderBaseURL(metaOf("searxng"), webToolsConfig{"baseUrls": obj{"searxng": "http://config.example"}}), "http://config.example")
		eq(t, resolveProviderBaseURL(metaOf("searxng"), webToolsConfig{}), searxngDefault)
	})
	t.Run("createSearchProvider names an unknown provider", func(t *testing.T) {
		_, err := createSearchProvider("nope", "", "")
		eq(t, errText(err), `Unknown search provider: "nope"`)
	})
}

func TestConfigFileExtra(t *testing.T) {
	t.Run("a relative XDG_CONFIG_HOME is ignored", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("XDG_CONFIG_HOME", "relative/dir")
		eq(t, configPath(), filepath.Join(os.Getenv("HOME"), ".config", "rpiv-web-tools", "config.json"))
	})
	t.Run("an absolute XDG_CONFIG_HOME is used, and the legacy file is the fallback", func(t *testing.T) {
		clearEnv(t)
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		eq(t, configPath(), filepath.Join(xdg, "rpiv-web-tools", "config.json"))
		legacy := filepath.Join(os.Getenv("HOME"), ".config", "rpiv-web-tools")
		os.MkdirAll(legacy, 0o755)
		os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"provider":"exa"}`), 0o644)
		eq(t, readConfig(), webToolsConfig{"provider": "exa"})
		os.MkdirAll(filepath.Join(xdg, "rpiv-web-tools"), 0o755)
		os.WriteFile(configPath(), []byte(`{"provider":"jina"}`), 0o644)
		eq(t, readConfig(), webToolsConfig{"provider": "jina"})
	})
	t.Run("saving over a world-readable file makes it private", func(t *testing.T) {
		clearEnv(t)
		writeRaw(t, "{}")
		os.Chmod(configPath(), 0o644)
		writeConfig(webToolsConfig{"provider": "brave"})
		info, _ := os.Stat(configPath())
		eq(t, info.Mode().Perm(), os.FileMode(0o600))
	})
	t.Run("a guidance list with an empty line is rejected as a whole", func(t *testing.T) {
		eq(t, validateGuidanceFields(obj{"promptGuidelines": arr{"ok", ""}}).HasGuidelines, false)
		eq(t, validateGuidanceFields(obj{"promptGuidelines": arr{"ok", "fine"}}).HasGuidelines, true)
	})
}

func TestProviderExtra(t *testing.T) {
	t.Run("a refused audio body", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "audio/mpeg"}, Body: "x"}
		})
		_, _, err := fetchURL(t, "https://example.com/a.mp3", false)
		eq(t, errText(err), "Unsupported content type: audio/mpeg. web_fetch supports text pages only.")
	})
	t.Run("the private ranges include 172.31 and fd00::, and stop at 172.32", func(t *testing.T) {
		eq(t, isPrivateOrLoopbackHostname("172.31.255.255"), true)
		eq(t, isPrivateOrLoopbackHostname("172.32.0.1"), false)
		eq(t, isPrivateOrLoopbackHostname("fd00::1"), true)
		eq(t, isPrivateOrLoopbackHostname("fc00::1"), true)
		eq(t, isPrivateOrLoopbackHostname("2001:db8::1"), false)
	})
	t.Run("exa fetch sends the character limit and keeps the title", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "exa", "apiKeys": obj{"exa": "K"}})
		n := useNet(t, func(netReq) netResp { return jsonResp(obj{"results": arr{obj{"text": "BODY", "title": "TTL"}}}) })
		_, details, err := fetchURL(t, "https://example.com/p", false)
		eq(t, err, nil)
		eq(t, n.Requests[0].Body, `{"ids":["https://example.com/p"],"text":{"maxCharacters":1000000}}`)
		eq(t, detailsMap(t, details)["title"], "TTL")
	})
	t.Run("youcom fetch asks for markdown", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "youcom", "apiKeys": obj{"youcom": "K"}})
		n := useNet(t, func(netReq) netResp { return jsonResp(arr{obj{"markdown": "BODY"}}) })
		fetchURL(t, "https://example.com/p", false)
		eq(t, n.Requests[0].Body, `{"urls":["https://example.com/p"],"formats":["markdown"]}`)
	})
	t.Run("youcom takes the first snippet, even an empty one, over the description", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "youcom", "apiKeys": obj{"youcom": "K"}})
		useNet(t, func(netReq) netResp {
			return jsonResp(obj{"results": obj{"web": arr{obj{"title": "A", "url": "u", "snippets": arr{"first", "second"}, "description": "desc"}, obj{"title": "B", "url": "v", "snippets": arr{""}, "description": "desc"}, obj{"title": "C", "url": "w", "description": "desc"}}}})
		})
		text, _, _ := search(t, "q", nil, nil)
		eq(t, strings.Contains(text, "1. **A**\n   u\n   first"), true)
		eq(t, strings.Contains(text, "2. **B**\n   v\n   \n"), true)
		eq(t, strings.Contains(text, "3. **C**\n   w\n   desc"), true)
	})
	t.Run("firecrawl says scrape failed when the failure carries no message", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "firecrawl", "apiKeys": obj{"firecrawl": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"success": false}) })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Firecrawl Fetch API error: scrape failed")
	})
	t.Run("jina reads nested results, caps them and encodes the query", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "jina", "apiKeys": obj{"jina": "K"}})
		var items arr
		for i := 0; i < 7; i++ {
			items = append(items, obj{"title": "T" + itoa(i), "url": "u", "description": "d"})
		}
		n := useNet(t, func(netReq) netResp { return jsonResp(obj{"data": obj{"results": items}}) })
		five := 5.0
		text, details, _ := search(t, "a/b&c d", &five, nil)
		eq(t, n.Requests[0].URL, "https://s.jina.ai/a%2Fb%26c%20d?num=5")
		eq(t, detailsMap(t, details)["resultCount"], float64(5))
		eq(t, strings.Contains(text, "T5"), false)
	})
	t.Run("ollama treats 0.0.0.0 and [::1] as local", func(t *testing.T) {
		for _, h := range []string{"http://0.0.0.0:11434", "http://[::1]:11434", "http://127.0.0.1:11434", "http://localhost:1"} {
			p, err := newOllama("", h)
			eq(t, err, nil)
			eq(t, p.local, true)
		}
		p, _ := newOllama("", "https://ollama.com")
		eq(t, p.local, false)
	})
	t.Run("web_search reports progress before it queries", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "K")
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		var updates []string
		_, _, err := searchTool(context.Background(), "go", nil, nil, func(text string, details any) { updates = append(updates, text) })
		eq(t, err, nil)
		eq(t, updates, []string{`Searching Brave for: "go"...`})
	})
	t.Run("web_fetch spills to a rpiv-fetch- temp directory", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: strings.Repeat("x\n", 3000)}
		})
		_, details, _ := fetchURL(t, "https://example.com/big", false)
		path := detailsMap(t, details)["fullOutputPath"].(string)
		eq(t, strings.HasPrefix(filepath.Base(filepath.Dir(path)), "rpiv-fetch-"), true)
		eq(t, filepath.Base(path), "content.txt")
	})
}
