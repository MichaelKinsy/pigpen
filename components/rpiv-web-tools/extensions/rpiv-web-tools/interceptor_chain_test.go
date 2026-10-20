package rpiv_web_tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The twins of providers/interceptors/chain.test.ts: the chain end to end through web_fetch. The original
// builds the chain in registerWebTools(pi, opts); here buildInterceptors(consumer) does it.

// chainFetch writes the config, builds the chain with the consumer default, and fetches url from a page
// that answers body.
func chainFetch(t *testing.T, cfg obj, consumer *bool, body, url string) string {
	t.Helper()
	clearEnv(t)
	t.Setenv(braveKeyEnv, "k")
	writeConfigFile(t, cfg)
	useNet(t, func(netReq) netResp {
		return netResp{Header: map[string]string{"Content-Type": "text/html"}, Body: "<html><body>" + body + "</body></html>"}
	})
	t.Cleanup(resetWebToolsInterceptors)
	buildInterceptors(consumer)
	text, _, err := fetchTool(context.Background(), url, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestInterceptorChainOptIn(t *testing.T) {
	const f = "providers/interceptors/chain"
	tw(t, f, "default OFF: github URL hits provider fetch when neither user nor consumer opts in", func(t *testing.T) {
		text := chainFetch(t, obj{"provider": "brave"}, nil, "plain page", "https://github.com/owner/repo")
		ghHas(t, text, "plain page")
	})
	tw(t, f, "consumer:true enables the interceptor when user config is absent", func(t *testing.T) {
		text := chainFetch(t, obj{"provider": "brave"}, new(true), "provider fallback", "https://github.com/owner/repo/issues")
		ghHas(t, text, "provider fallback")
		if getActiveGitHubInterceptor() == nil {
			t.Fatal("the consumer default did not enable the interceptor")
		}
	})
	tw(t, f, "user config false beats consumer true (explicit user disable)", func(t *testing.T) {
		text := chainFetch(t, obj{"provider": "brave", "interceptors": obj{"github": false}}, new(true), "provider only", "https://github.com/owner/repo/blob/main/x.ts")
		ghHas(t, text, "provider only")
		eq(t, len(getInterceptors()), 0)
	})
	tw(t, f, "user object form implies opt-in even when consumer left it unset", func(t *testing.T) {
		text := chainFetch(t, obj{"provider": "brave", "interceptors": obj{"github": obj{"maxRepoSizeMB": 999.0}}}, nil, "provider fallback", "https://github.com/owner/repo/issues")
		ghHas(t, text, "provider fallback")
		g := getActiveGitHubInterceptor()
		if g == nil || g.options.MaxRepoSizeMB != 999 {
			t.Fatalf("the object form did not enable the interceptor: %+v", g)
		}
	})
}

func TestInterceptorChainFallThrough(t *testing.T) {
	const f = "providers/interceptors/chain"
	tw(t, f, "interceptor returning null falls through to provider fetch", func(t *testing.T) {
		text := chainFetch(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, nil, "fallback body", "https://github.com/owner/repo/issues")
		ghHas(t, text, "fallback body")
	})
	tw(t, f, "empty chain (interceptor disabled) is a no-op — every URL hits the provider", func(t *testing.T) {
		text1 := chainFetch(t, obj{"provider": "brave"}, nil, "vanilla", "https://example.com/")
		text2 := chainFetch(t, obj{"provider": "brave"}, nil, "vanilla", "https://github.com/owner/repo/blob/main/file.ts")
		ghHas(t, text1, "vanilla")
		ghHas(t, text2, "vanilla")
	})
}

// Beyond the original's cases: the chain answers from the interceptor, and a failed interceptor falls
// through to the page.
func TestInterceptorChainAnswers(t *testing.T) {
	setup := func(t *testing.T, cfg obj, handler func(netReq) netResp) *fakeNet {
		t.Helper()
		clearEnv(t)
		t.Setenv(braveKeyEnv, "k")
		writeConfigFile(t, cfg)
		n := useNet(t, handler)
		t.Cleanup(resetWebToolsInterceptors)
		buildInterceptors(nil)
		return n
	}
	page := func(netReq) netResp {
		return netResp{Header: map[string]string{"Content-Type": "text/html"}, Body: "<html><body>the page</body></html>"}
	}
	t.Run("the clone view is the fetch result and no request is made", func(t *testing.T) {
		n := setup(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, page)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hi"), 0o644)
		getActiveGitHubInterceptor().seedCache("owner/repo", dir, ghResolved(dir))
		ctx, _ := ghExec(t, ghMakeUnavailable)
		text, details, err := fetchTool(ctx, "https://github.com/owner/repo", false, nil)
		eq(t, err, nil)
		ghHas(t, text, "**Fetched:** https://github.com/owner/repo\n**Title:** owner/repo\n**Content-Type:** text/plain\n\n")
		ghHas(t, text, "Repository cloned to: "+dir)
		eq(t, detailsMap(t, details)["title"], "owner/repo")
		eq(t, len(n.Requests), 0)
	})
	t.Run("raw does not bypass the interceptor, as in the original", func(t *testing.T) {
		n := setup(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, page)
		dir := t.TempDir()
		getActiveGitHubInterceptor().seedCache("owner/repo", dir, ghResolved(dir))
		ctx, _ := ghExec(t, ghMakeUnavailable)
		text, _, err := fetchTool(ctx, "https://github.com/owner/repo", true, nil)
		eq(t, err, nil)
		ghHas(t, text, "Repository cloned to:")
		eq(t, len(n.Requests), 0)
	})
	t.Run("an interceptor that finds nothing falls through to the page", func(t *testing.T) {
		n := setup(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, page)
		ctx, _ := ghExec(t, ghMakeUnavailable)
		text, _, err := fetchTool(ctx, "https://github.com/owner/repo", false, nil)
		eq(t, err, nil)
		ghHas(t, text, "the page")
		eq(t, len(n.Requests), 1)
	})
	t.Run("without a host command runner the interceptor finds nothing and the page is fetched", func(t *testing.T) {
		setup(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, page)
		text, _, err := fetchTool(context.Background(), "https://github.com/owner/repo/blob/main/x.go", false, nil)
		eq(t, err, nil)
		ghHas(t, text, "the page")
	})
	t.Run("the chain is built when the extension registers, not when a tool runs", func(t *testing.T) {
		setup(t, obj{"provider": "brave"}, page)
		writeConfigFile(t, obj{"provider": "brave", "interceptors": obj{"github": true}})
		eq(t, len(getInterceptors()), 0)
		buildInterceptors(nil)
		eq(t, len(getInterceptors()), 1)
		eq(t, getInterceptors()[0].name(), "github")
	})
	t.Run("clearCloneCache removes the clone directories and the cache entries", func(t *testing.T) {
		setup(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, page)
		dir := filepath.Join(t.TempDir(), "clone")
		os.MkdirAll(dir, 0o755)
		g := getActiveGitHubInterceptor()
		g.seedCache("owner/repo", dir, ghResolved(dir))
		clearCloneCache()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("the clone directory is still there")
		}
		eq(t, strings.Contains(strings.Join(g.cacheKeys(), ","), "owner/repo"), false)
	})
}
