package webmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

func TestWebSearchTavily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Method != http.MethodPost {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var req struct {
			APIKey     string `json:"api_key"`
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.APIKey != "test-tavily-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go Docs","url":"https://go.dev","content":"The Go programming language"},{"title":"PiG","url":"https://example.com/pig","content":"Extension host"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TAVILY_API_KEY", "test-tavily-key")
	t.Setenv("HERMES_WEB_SEARCH_BASE_URL", srv.URL)

	res := handleWebSearch(context.Background(), map[string]any{
		"query":    "golang",
		"limit":    5,
		"provider": "tavily",
	}, config.Default())

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[Go Docs](https://go.dev)") {
		t.Fatalf("missing expected result in markdown:\n%s", res.Content)
	}
	sr, ok := res.StructuredContent.(SearchResponse)
	if !ok {
		t.Fatalf("expected SearchResponse structured content, got %T", res.StructuredContent)
	}
	if sr.Provider != "tavily" || len(sr.Results) != 2 {
		t.Fatalf("unexpected search response: %+v", sr)
	}
}

func TestWebSearchBrave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Subscription-Token") != "brave-secret" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("q") != "hermes" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Hermes","url":"https://hermes.example","description":"Agent framework"}]}}`))
	}))
	defer srv.Close()

	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("BRAVE_API_KEY", "brave-secret")
	t.Setenv("HERMES_WEB_SEARCH_BASE_URL", srv.URL)

	res := handleWebSearch(context.Background(), map[string]any{
		"query": "hermes",
	}, config.Default())

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	sr := res.StructuredContent.(SearchResponse)
	if sr.Provider != "brave" || len(sr.Results) != 1 || sr.Results[0].Title != "Hermes" {
		t.Fatalf("unexpected brave response: %+v", sr)
	}
}

func TestWebSearchSearXNG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			http.Error(w, "bad searxng request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"SearXNG Hit","url":"https://searx.example","content":"Privacy search"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("BRAVE_API_KEY", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	t.Setenv("SEARXNG_URL", srv.URL)

	res := handleWebSearch(context.Background(), map[string]any{
		"query":    "privacy",
		"provider": "auto",
	}, config.Default())

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	sr := res.StructuredContent.(SearchResponse)
	if sr.Provider != "searxng" || len(sr.Results) != 1 || sr.Results[0].URL != "https://searx.example" {
		t.Fatalf("unexpected searxng response: %+v", sr)
	}
}

func TestWebSearchDuckDuckGoAndLimitCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"Heading": "Go",
			"AbstractText": "Statically typed compiled language.",
			"AbstractURL": "https://go.dev",
			"RelatedTopics": [
				{"Text": "Go Tour - Interactive tour", "FirstURL": "https://go.dev/tour"},
				{"Topics": [{"Text": "Go Blog - Official blog", "FirstURL": "https://go.dev/blog"}]}
			]
		}`))
	}))
	defer srv.Close()

	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("BRAVE_API_KEY", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	t.Setenv("HERMES_WEB_SEARCH_BASE_URL", srv.URL)

	res := handleWebSearch(context.Background(), map[string]any{
		"query":    "go",
		"limit":    2,
		"provider": "duckduckgo",
	}, config.Default())

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	sr := res.StructuredContent.(SearchResponse)
	if sr.Provider != "duckduckgo" || len(sr.Results) != 2 {
		t.Fatalf("expected 2 capped results, got: %+v", sr)
	}
}

func TestWebSearchErrors(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")

	res := handleWebSearch(context.Background(), map[string]any{}, config.Default())
	if !res.IsError || !strings.Contains(res.Content, "query is required") {
		t.Fatalf("expected missing query error, got: %+v", res)
	}

	res = handleWebSearch(context.Background(), map[string]any{
		"query":    "hello",
		"provider": "tavily",
	}, config.Default())
	if !res.IsError || !strings.Contains(res.Content, "TAVILY_API_KEY") {
		t.Fatalf("expected missing key error, got: %+v", res)
	}

	res = handleWebSearch(context.Background(), map[string]any{
		"query":    "hello",
		"provider": "unknown-engine",
	}, config.Default())
	if !res.IsError || !strings.Contains(res.Content, "unsupported provider") {
		t.Fatalf("expected unsupported provider error, got: %+v", res)
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	defer errSrv.Close()
	t.Setenv("HERMES_WEB_SEARCH_BASE_URL", errSrv.URL)

	res = handleWebSearch(context.Background(), map[string]any{
		"query":    "boom",
		"provider": "duckduckgo",
	}, config.Default())
	if !res.IsError || !strings.Contains(res.Content, "HTTP 502") {
		t.Fatalf("expected HTTP 502 error, got: %+v", res)
	}
}

func TestWebExtractHTMLAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head>
  <title>Sample &amp; Page</title>
  <style>body { color: red; }</style>
  <script>console.log("secret_script_token");</script>
</head>
<body>
  <h1>Main Heading</h1>
  <p>Hello &lt;world&gt; from <a href="https://example.com/docs">the docs</a>.</p>
  <ul><li>First item</li><li>Second item</li></ul>
</body>
</html>`))
	}))
	defer srv.Close()

	// Without HERMES_ALLOW_LOCALHOST_FETCH=1, loopback should be blocked.
	t.Setenv("HERMES_ALLOW_LOCALHOST_FETCH", "")
	blocked := handleWebExtract(context.Background(), map[string]any{"url": srv.URL})
	if !blocked.IsError || !strings.Contains(blocked.Content, "blocked loopback") {
		t.Fatalf("expected SSRF loopback block, got: %+v", blocked)
	}

	// With HERMES_ALLOW_LOCALHOST_FETCH=1, extraction succeeds.
	t.Setenv("HERMES_ALLOW_LOCALHOST_FETCH", "1")
	res := handleWebExtract(context.Background(), map[string]any{
		"urls":      []any{srv.URL},
		"max_chars": 20000,
	})
	if res.IsError {
		t.Fatalf("unexpected extract error: %s", res.Content)
	}
	if strings.Contains(res.Content, "secret_script_token") || strings.Contains(res.Content, "color: red") {
		t.Fatalf("script/style was not stripped:\n%s", res.Content)
	}
	for _, want := range []string{"# Sample & Page", "# Main Heading", "Hello <world> from [the docs](https://example.com/docs).", "- First item", "- Second item"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("expected %q in extracted content:\n%s", want, res.Content)
		}
	}

	// Truncation check.
	truncRes := handleWebExtract(context.Background(), map[string]any{
		"url":       srv.URL,
		"max_chars": 25,
	})
	if truncRes.IsError {
		t.Fatalf("unexpected truncation test error: %s", truncRes.Content)
	}
	er := truncRes.StructuredContent.(ExtractResponse)
	if len(er.Pages) != 1 || !er.Pages[0].Truncated || !strings.Contains(er.Pages[0].Content, "[truncated]") {
		t.Fatalf("expected truncated page, got: %+v", er)
	}
}

func TestWebExtractGuardsAndLimits(t *testing.T) {
	t.Setenv("HERMES_ALLOW_LOCALHOST_FETCH", "1")

	res := handleWebExtract(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if !res.IsError || !strings.Contains(res.Content, "unsupported URL scheme") {
		t.Fatalf("expected scheme guard error, got: %+v", res)
	}

	res = handleWebExtract(context.Background(), map[string]any{
		"urls": []string{"https://a.example", "https://b.example", "https://c.example", "https://d.example", "https://e.example", "https://f.example"},
	})
	if !res.IsError || !strings.Contains(res.Content, "maximum 5 URLs") {
		t.Fatalf("expected max URLs error, got: %+v", res)
	}

	res = handleWebExtract(context.Background(), map[string]any{"url": "http://169.254.169.254/latest/meta-data/"})
	if !res.IsError || !strings.Contains(res.Content, "blocked non-public IP") {
		t.Fatalf("expected metadata IP block, got: %+v", res)
	}
}

func TestExecuteCodeBashSuccessTimeoutAndTruncation(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	// Success case.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := handleExecuteCode(ctx, map[string]any{
		"code":     "echo hello-hermes",
		"language": "bash",
	}, 10, 50000)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	cr := res.StructuredContent.(CodeExecResult)
	if cr.Status != "ok" || cr.ExitCode != 0 || strings.TrimSpace(cr.Output) != "hello-hermes" {
		t.Fatalf("unexpected exec result: %+v", cr)
	}

	// Non-zero exit code.
	res = handleExecuteCode(ctx, map[string]any{
		"code":     "echo err-msg >&2; exit 7",
		"language": "bash",
	}, 10, 50000)
	if !res.IsError {
		t.Fatalf("expected IsError=true for non-zero exit")
	}
	cr = res.StructuredContent.(CodeExecResult)
	if cr.ExitCode != 7 || !strings.Contains(cr.Stderr, "err-msg") {
		t.Fatalf("unexpected non-zero result: %+v", cr)
	}

	// Truncation case.
	res = handleExecuteCode(ctx, map[string]any{
		"code":     "printf '%0200d' 0",
		"language": "bash",
	}, 10, 40)
	if res.IsError {
		t.Fatalf("unexpected truncation error: %s", res.Content)
	}
	cr = res.StructuredContent.(CodeExecResult)
	if !cr.Truncated || !strings.Contains(cr.Output, "[output truncated]") {
		t.Fatalf("expected truncated output, got: %+v", cr)
	}

	// Timeout case (spawns background child in same process group to test cleanup).
	tCtx, tCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer tCancel()
	res = handleExecuteCode(tCtx, map[string]any{
		"code":     "sleep 10 & wait",
		"language": "bash",
	}, 1, 50000)
	if !res.IsError {
		t.Fatalf("expected timeout error")
	}
	cr = res.StructuredContent.(CodeExecResult)
	if cr.Status != "timeout" || cr.ExitCode != 124 {
		t.Fatalf("expected timeout status, got: %+v", cr)
	}
}

func TestExecuteCodePython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := handleExecuteCode(ctx, map[string]any{
		"code": "print(6 * 7)",
	}, 10, 50000)
	if res.IsError {
		t.Fatalf("unexpected python error: %s", res.Content)
	}
	cr := res.StructuredContent.(CodeExecResult)
	if cr.Language != "python" || strings.TrimSpace(cr.Output) != "42" {
		t.Fatalf("unexpected python result: %+v", cr)
	}
}

func TestTranslateAndRegisterHermesMCPServers(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "tok-123")
	t.Setenv("MCP_STDIO_KEY", "val-abc")

	cfg := config.Default()
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"stdio_srv": {
			Name:    "stdio_srv",
			Command: "npx",
			Args:    []string{"-y", "@example/mcp-server"},
			Env:     map[string]string{"API_KEY": "${MCP_STDIO_KEY}"},
			Enabled: true,
		},
		"http_srv": {
			Name:    "http_srv",
			URL:     "https://mcp.example.com/v1",
			Headers: map[string]string{"Authorization": "Bearer ${env:MCP_TEST_TOKEN}"},
			Enabled: true,
		},
		"disabled_srv": {
			Name:    "disabled_srv",
			Command: "disabled-bin",
			Enabled: false,
		},
		"invalid_srv": {
			Name:    "invalid_srv",
			Enabled: true,
		},
	}

	// Verify translation directly.
	stdioCfg, ok, reason := TranslateMCPServerConfig("stdio_srv", cfg.MCPServers["stdio_srv"])
	if !ok || reason != "" {
		t.Fatalf("stdio translation failed: %s", reason)
	}
	if stdioCfg.Type != "stdio" || stdioCfg.Command != "npx" || len(stdioCfg.Args) != 2 {
		t.Fatalf("unexpected stdio config: %+v", stdioCfg)
	}
	if v, ok := stdioCfg.Env.Get("API_KEY"); !ok || v != "val-abc" {
		t.Fatalf("expected expanded env var, got %q (%v)", v, ok)
	}

	httpCfg, ok, reason := TranslateMCPServerConfig("http_srv", cfg.MCPServers["http_srv"])
	if !ok || reason != "" {
		t.Fatalf("http translation failed: %s", reason)
	}
	if httpCfg.Type != "http" || httpCfg.URL != "https://mcp.example.com/v1" {
		t.Fatalf("unexpected http config: %+v", httpCfg)
	}
	if v, ok := httpCfg.Headers.Get("Authorization"); !ok || v != "Bearer tok-123" {
		t.Fatalf("expected expanded header, got %q (%v)", v, ok)
	}

	if _, ok, reason := TranslateMCPServerConfig("disabled_srv", cfg.MCPServers["disabled_srv"]); ok || !strings.Contains(reason, "disabled") {
		t.Fatalf("expected disabled skip, got ok=%v reason=%q", ok, reason)
	}

	// Verify RegisterHermesMCPServers queues into sdk.Extension without error.
	ext := sdk.New("test-pig-hermes")
	diags := RegisterHermesMCPServers(ext, cfg)
	if len(diags) != 4 {
		t.Fatalf("expected 4 diagnostics, got %+v", diags)
	}

	byName := make(map[string]MCPRegistrationDiagnostic, len(diags))
	for _, d := range diags {
		byName[d.Name] = d
	}
	if byName["stdio_srv"].Action != "registered" || byName["stdio_srv"].Message != "stdio" {
		t.Fatalf("unexpected stdio diag: %+v", byName["stdio_srv"])
	}
	if byName["http_srv"].Action != "registered" || byName["http_srv"].Message != "http" {
		t.Fatalf("unexpected http diag: %+v", byName["http_srv"])
	}
	if byName["disabled_srv"].Action != "skipped" {
		t.Fatalf("unexpected disabled diag: %+v", byName["disabled_srv"])
	}
	if byName["invalid_srv"].Action != "skipped" {
		t.Fatalf("unexpected invalid diag: %+v", byName["invalid_srv"])
	}

	// Smoke test top-level Register.
	ext2 := sdk.New("test-register")
	Register(ext2, paths.ProfilePaths{}, cfg)
}

func TestWebSearchDotEnvFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			APIKey string `json:"api_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.APIKey != "dotenv-tavily-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"DotEnv","url":"https://dotenv.example","content":"Loaded from .env"}]}`))
	}))
	defer srv.Close()

	for _, k := range []string{"TAVILY_API_KEY", "BRAVE_API_KEY", "BRAVE_SEARCH_API_KEY", "SEARXNG_URL", "HERMES_WEB_SEARCH_BASE_URL"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	cfg := config.Default()
	cfg.Raw["env.TAVILY_API_KEY"] = "dotenv-tavily-key"
	cfg.Raw["env.HERMES_WEB_SEARCH_BASE_URL"] = srv.URL

	res := handleWebSearch(context.Background(), map[string]any{
		"query": "dotenv test",
	}, cfg)
	if res.IsError {
		t.Fatalf("unexpected error using .env fallback: %s", res.Content)
	}
	sr := res.StructuredContent.(SearchResponse)
	if sr.Provider != "tavily" || len(sr.Results) != 1 || sr.Results[0].Title != "DotEnv" {
		t.Fatalf("unexpected dotenv search response: %+v", sr)
	}
}

func TestMCPValidationCollisionsAndDotEnv(t *testing.T) {
	for _, k := range []string{"DOTENV_MCP_TOKEN", "UNSET_MCP_HOST"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	cfg := config.Default()
	cfg.Raw["env.DOTENV_MCP_TOKEN"] = "from-dotenv-secret"
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"bad name!": {
			Command: "npx",
			Enabled: true,
		},
		"bad_scheme": {
			URL:     "ftp://mcp.example.com/v1",
			Enabled: true,
		},
		"unresolved_url": {
			URL:     "https://${UNSET_MCP_HOST}/v1",
			Enabled: true,
		},
		"my-server": {
			URL:     "https://mcp.example.com/v1",
			Headers: map[string]string{"Authorization": "Bearer ${DOTENV_MCP_TOKEN}"},
			Enabled: true,
		},
		"my_server": {
			URL:     "https://mcp.example.com/v2",
			Enabled: true,
		},
	}

	// Check .env expansion directly.
	httpCfg, ok, reason := TranslateMCPServerConfig("my-server", cfg.MCPServers["my-server"], cfg)
	if !ok || reason != "" {
		t.Fatalf("expected my-server to translate cleanly, got reason=%q", reason)
	}
	if v, ok := httpCfg.Headers.Get("Authorization"); !ok || v != "Bearer from-dotenv-secret" {
		t.Fatalf("expected .env header expansion, got %q (%v)", v, ok)
	}

	ext := sdk.New("test-mcp-validation")
	diags := RegisterHermesMCPServers(ext, cfg)
	byName := make(map[string]MCPRegistrationDiagnostic, len(diags))
	for _, d := range diags {
		byName[d.Name] = d
	}
	if byName["bad name!"].Action != "skipped" || !strings.Contains(byName["bad name!"].Message, "invalid server name") {
		t.Fatalf("expected invalid name skip, got %+v", byName["bad name!"])
	}
	if byName["bad_scheme"].Action != "skipped" || !strings.Contains(byName["bad_scheme"].Message, "unsupported url scheme") {
		t.Fatalf("expected scheme skip, got %+v", byName["bad_scheme"])
	}
	if byName["unresolved_url"].Action != "skipped" || !strings.Contains(byName["unresolved_url"].Message, "unresolved") {
		t.Fatalf("expected unresolved placeholder skip, got %+v", byName["unresolved_url"])
	}
	if byName["my-server"].Action != "registered" {
		t.Fatalf("expected my-server to register, got %+v", byName["my-server"])
	}
	if byName["my_server"].Action != "skipped" || !strings.Contains(byName["my_server"].Message, "collides") {
		t.Fatalf("expected normalized collision skip for my_server, got %+v", byName["my_server"])
	}
}

func TestExecuteCodeBackgroundChildDoesNotHang(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	res := handleExecuteCode(ctx, map[string]any{
		"code":     "sleep 30 & echo fast-done",
		"language": "bash",
	}, 15, 50000)
	elapsed := time.Since(start)
	if elapsed > 6*time.Second {
		t.Fatalf("execute_code hung for %v despite WaitDelay (result: %+v)", elapsed, res)
	}
	cr := res.StructuredContent.(CodeExecResult)
	if !strings.Contains(cr.Output, "fast-done") {
		t.Fatalf("expected fast-done in output, got: %+v", cr)
	}
}
