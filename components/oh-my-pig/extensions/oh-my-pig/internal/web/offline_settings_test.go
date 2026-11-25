package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// refusingClient returns an *http.Client whose dialer records every connection
// attempt and immediately refuses it. Used to prove offline / disabled paths
// never touch the network.
func refusingClient(attempts *atomic.Int64) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				attempts.Add(1)
				return nil, errors.New("refused: network dial attempted to " + network + "://" + addr)
			},
		},
		Timeout: 5 * time.Second,
	}
}

func withRefusingHTTPClient(t *testing.T) *atomic.Int64 {
	t.Helper()
	var attempts atomic.Int64
	prev := httpClient
	httpClient = refusingClient(&attempts)
	t.Cleanup(func() { httpClient = prev })
	return &attempts
}

// upstream: packages/coding-agent/src/web/search/index.ts:330-341,510
func TestOfflineSkipsAllProvidersAndContactsNoHost(t *testing.T) {
	const wantMsg = "No web search backend is available on the active model's provider under --offline."

	cases := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{
			name: "PI_OFFLINE=1",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{"providers.webKeyless": true})
				t.Setenv("PI_OFFLINE", "1")
			},
		},
		{
			name: "PI_OFFLINE=true",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{"providers.webKeyless": true})
				t.Setenv("PI_OFFLINE", "true")
			},
		},
		{
			name: "PIG_OFFLINE=1",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{"providers.webKeyless": true})
				t.Setenv("PIG_OFFLINE", "1")
			},
		},
		{
			name: "models.offline setting",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{
					"models.offline":       true,
					"providers.webKeyless": true,
				})
			},
		},
		{
			name: "offline with credentialed providers configured",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{"providers.webKeyless": true})
				t.Setenv("PI_OFFLINE", "1")
				t.Setenv("EXA_API_KEY", "exa-key")
				t.Setenv("BRAVE_API_KEY", "brave-key")
				t.Setenv("PARALLEL_API_KEY", "parallel-key")
				t.Setenv("FIRECRAWL_API_KEY", "fc-key")
				t.Setenv("SEARXNG_ENDPOINT", "https://searx.example.com")
			},
		},
		{
			name: "offline with HN-only site query",
			setup: func(t *testing.T) {
				withSettings(t, map[string]any{"providers.webKeyless": true})
				t.Setenv("PI_OFFLINE", "1")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			tc.setup(t)
			attempts := withRefusingHTTPClient(t)

			query := "golang release"
			if strings.Contains(tc.name, "HN-only") {
				query = "golang site:news.ycombinator.com"
			}
			text, details := ExecuteSearch(context.Background(), SearchParams{Query: query})
			if got := attempts.Load(); got != 0 {
				t.Fatalf("offline search attempted %d network connection(s), want 0", got)
			}
			if text != "Error: "+wantMsg {
				t.Fatalf("text = %q, want %q", text, "Error: "+wantMsg)
			}
			if details.Response.Provider != "none" || details.Error != wantMsg || len(details.Failures) != 0 {
				t.Fatalf("unexpected details: %+v", details)
			}
		})
	}
}

// upstream: packages/coding-agent/src/web/search/index.ts:318,371-374,514-515
func TestWebKeylessSettingHookAndNoEnvOverride(t *testing.T) {
	t.Run("providers.webKeyless=false skips keyless providers with zero network attempts", func(t *testing.T) {
		clearProviderEnv(t)
		withSettings(t, map[string]any{"providers.webKeyless": false})
		attempts := withRefusingHTTPClient(t)

		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "golang"})
		if got := attempts.Load(); got != 0 {
			t.Fatalf("attempted %d network connection(s), want 0", got)
		}
		want := "No configured web search service is available, and providers.webKeyless is off. Configure a search credential or turn providers.webKeyless on."
		if text != "Error: "+want || details.Error != want || details.Response.Provider != "none" {
			t.Fatalf("got text=%q details=%+v", text, details)
		}
	})

	t.Run("OMP_PROVIDERS_WEB_KEYLESS env is ignored (omp has no env var for providers.webKeyless)", func(t *testing.T) {
		clearProviderEnv(t)
		// Unassigned Setting defaults providers.webKeyless to true (omp default).
		prev := Setting
		Setting = nil
		t.Cleanup(func() { Setting = prev })
		t.Setenv("OMP_PROVIDERS_WEB_KEYLESS", "false")

		if !webKeylessEnabled(context.Background()) {
			t.Fatalf("webKeylessEnabled returned false; OMP_PROVIDERS_WEB_KEYLESS must not override omp's setting")
		}
	})

	t.Run("handleWebSearch forwards sdk.Context to Setting hook", func(t *testing.T) {
		clearProviderEnv(t)
		attempts := withRefusingHTTPClient(t)

		var sawOfflineKey bool
		prev := Setting
		Setting = func(ctx sdk.Context, key string) any {
			if key == "models.offline" {
				sawOfflineKey = true
				return true
			}
			return nil
		}
		t.Cleanup(func() { Setting = prev })

		res, err := handleWebSearch(sdk.Context{}, map[string]any{"query": "hello"})
		if err != nil {
			t.Fatalf("handleWebSearch err: %v", err)
		}
		if got := attempts.Load(); got != 0 {
			t.Fatalf("attempted %d network connection(s), want 0", got)
		}
		if !sawOfflineKey {
			t.Fatalf("Setting hook was not queried for models.offline")
		}
		tr, ok := res.(sdk.ToolResult)
		if !ok {
			t.Fatalf("result type = %T, want sdk.ToolResult", res)
		}
		want := "Error: No web search backend is available on the active model's provider under --offline."
		if tr.Content != want {
			t.Fatalf("Content = %q, want %q", tr.Content, want)
		}
	})
}

// upstream: packages/coding-agent/src/web/search/index.ts:307-315
// upstream: packages/coding-agent/src/web/search/providers/exa.ts:458-467
// upstream: packages/coding-agent/src/web/search/providers/searxng.ts:106-141,400-455
func TestWebSettingsHookForTimeoutExaAndSearXNG(t *testing.T) {
	t.Run("providers.webSearchTimeoutSeconds configures and clamps timeout", func(t *testing.T) {
		clearProviderEnv(t)
		withSettings(t, map[string]any{"providers.webSearchTimeoutSeconds": float64(12.5)})
		if got := configuredSearchTimeout(context.Background()); got != 12500*time.Millisecond {
			t.Fatalf("timeout = %v, want 12.5s", got)
		}

		withSettings(t, map[string]any{"providers.webSearchTimeoutSeconds": float64(999)})
		if got := configuredSearchTimeout(context.Background()); got != MaxWebSearchTimeoutSeconds*time.Second {
			t.Fatalf("clamped timeout = %v, want %v", got, MaxWebSearchTimeoutSeconds*time.Second)
		}

		withSettings(t, map[string]any{"providers.webSearchTimeoutSeconds": float64(0)})
		if got := configuredSearchTimeout(context.Background()); got != DefaultWebSearchTimeoutSeconds*time.Second {
			t.Fatalf("zero fallback timeout = %v, want %v", got, DefaultWebSearchTimeoutSeconds*time.Second)
		}
	})

	t.Run("exa.enabled=false disables Exa even when EXA_API_KEY is set", func(t *testing.T) {
		clearProviderEnv(t)
		withSettings(t, map[string]any{
			"providers.webKeyless": false,
			"exa.enabled":          false,
		})
		t.Setenv("EXA_API_KEY", "exa-key")
		attempts := withRefusingHTTPClient(t)

		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "golang"})
		if got := attempts.Load(); got != 0 {
			t.Fatalf("attempted %d network connection(s), want 0", got)
		}
		if details.Response.Provider != "none" || !strings.Contains(text, "providers.webKeyless is off") {
			t.Fatalf("expected exa skipped; got text=%q details=%+v", text, details)
		}
	})

	t.Run("searxng.* settings configure endpoint, auth, and query params", func(t *testing.T) {
		clearProviderEnv(t)
		// clearProviderEnv sets SEARXNG_BASIC_* to "" (present, which is valid RFC 7617 Basic auth);
		// unset them so searxng.token is exercised.
		_ = os.Unsetenv("SEARXNG_BASIC_USERNAME")
		_ = os.Unsetenv("SEARXNG_BASIC_PASSWORD")
		var gotQuery, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"SX Result","url":"https://example.com/sx","content":"snippet"}]}`))
		}))
		t.Cleanup(srv.Close)

		prevClient := httpClient
		httpClient = srv.Client()
		t.Cleanup(func() { httpClient = prevClient })

		withSettings(t, map[string]any{
			"providers.webKeyless": false,
			"searxng.endpoint":     srv.URL,
			"searxng.token":        "secret-tok",
			"searxng.categories":   "general,it",
			"searxng.engines":      "duckduckgo",
			"searxng.language":     "en",
			"searxng.safesearch":   float64(1),
		})

		text, details := ExecuteSearch(context.Background(), SearchParams{Query: "golang"})
		if details.Error != "" || details.Response.Provider != "searxng" || !strings.Contains(text, "SX Result") {
			t.Fatalf("unexpected result: text=%q details=%+v", text, details)
		}
		if gotAuth != "Bearer secret-tok" {
			t.Fatalf("Authorization = %q, want Bearer secret-tok", gotAuth)
		}
		for _, wantPart := range []string{"categories=general%2Cit", "engines=duckduckgo", "language=en", "safesearch=1"} {
			if !strings.Contains(gotQuery, wantPart) {
				t.Fatalf("query %q missing %q", gotQuery, wantPart)
			}
		}
	})
}
