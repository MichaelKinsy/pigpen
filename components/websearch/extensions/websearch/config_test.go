package websearch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Twins of test/config-path.test.mjs, test/ssrf-allow-ranges-config.test.mjs and the config
// parts of test/fetch-content-domain-policy.test.mjs (upstream 9a734ed).
//
// Adaptation (PORT.md "host adaptations"): the new-config target is PiG's own directory
// ($PIG_HOME/agent, or $XDG_CONFIG_HOME/pig), and Pi's directories stay readable fallbacks,
// so an existing ~/.pi/agent/web-search.json keeps working. Expectations that name the
// target for an absent config say so in the case.

type dirs struct{ Dir, Path string }

func cfgDirs() dirs { return dirs{ConfigDir(), ConfigPath()} }

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pathEnv clears every directory selector, then sets HOME like the upstream child process.
func pathEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	unsetenv(t, "PI_CODING_AGENT_DIR", "PIG_CODING_AGENT_DIR", "PIG_HOME", "XDG_CONFIG_HOME", "PIG_WEBSEARCH_CONFIG")
	ResetCaches()
	t.Cleanup(ResetCaches)
	return root
}

func TestUpstream_config_path(t *testing.T) {
	const f = "config-path"

	tw(t, f, "web-search config path uses PI_CODING_AGENT_DIR before XDG_CONFIG_HOME", func(t *testing.T) {
		root := pathEnv(t)
		agent := filepath.Join(root, "agent-dir")
		xdg := filepath.Join(root, "xdg")
		home := filepath.Join(root, "home")
		writeJSON(t, filepath.Join(agent, "web-search.json"), `{"perplexityApiKey":"pplx-from-agent"}`)
		writeJSON(t, filepath.Join(home, ".pi", "web-search.json"), `{"perplexityApiKey":"pplx-from-legacy"}`)
		writeJSON(t, filepath.Join(xdg, "pi", "web-search.json"), `{}`)
		t.Setenv("PI_CODING_AGENT_DIR", agent)
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got := cfgDirs()
		if got != (dirs{agent, filepath.Join(agent, "web-search.json")}) || !PerplexityAvailable() {
			t.Fatalf("%+v available=%v", got, PerplexityAvailable())
		}
	})

	tw(t, f, "web-search config path prefers the Pi agent directory over legacy config", func(t *testing.T) {
		root := pathEnv(t)
		home := filepath.Join(root, "home")
		agent := filepath.Join(home, ".pi", "agent")
		writeJSON(t, filepath.Join(home, ".pi", "web-search.json"), `{"perplexityApiKey":"pplx-from-legacy"}`)
		writeJSON(t, filepath.Join(agent, "web-search.json"), `{"geminiApiKey":"gemini-from-agent"}`)
		got := cfgDirs()
		if got != (dirs{agent, filepath.Join(agent, "web-search.json")}) {
			t.Fatalf("%+v", got)
		}
		if !HasCredentialSource(CredentialOptions{Provider: "Gemini", ConfiguredValue: mustRoot(t)["geminiApiKey"]}) {
			t.Fatal("gemini key from the agent config must be visible")
		}
	})

	tw(t, f, "web-search config path falls back to legacy ~/.pi when agent config is absent", func(t *testing.T) {
		root := pathEnv(t)
		home := filepath.Join(root, "home")
		writeJSON(t, filepath.Join(home, ".pi", "web-search.json"), `{"perplexityApiKey":"pplx-from-legacy"}`)
		got := cfgDirs()
		if got != (dirs{filepath.Join(home, ".pi"), filepath.Join(home, ".pi", "web-search.json")}) || !PerplexityAvailable() {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "web-search config path defaults to the Pi agent directory when both files are absent", func(t *testing.T) {
		root := pathEnv(t)
		home := filepath.Join(root, "home")
		// Adapted: PiG's agent directory is the new-config target.
		want := dirs{filepath.Join(home, ".pig", "agent"), filepath.Join(home, ".pig", "agent", "web-search.json")}
		if got := cfgDirs(); got != want {
			t.Fatalf("%+v want %+v", got, want)
		}
	})

	tw(t, f, "web-search config path uses XDG_CONFIG_HOME pi directory when agent dir is unset", func(t *testing.T) {
		root := pathEnv(t)
		home, xdg := filepath.Join(root, "home"), filepath.Join(root, "xdg")
		writeJSON(t, filepath.Join(home, ".pi", "web-search.json"), `{"perplexityApiKey":"pplx-from-legacy"}`)
		writeJSON(t, filepath.Join(xdg, "pi", "web-search.json"), `{"geminiApiKey":"gemini-from-xdg"}`)
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got := cfgDirs()
		if got != (dirs{filepath.Join(xdg, "pi"), filepath.Join(xdg, "pi", "web-search.json")}) {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "web-search config path keeps the legacy fallback stable after XDG config is created", func(t *testing.T) {
		root := pathEnv(t)
		home, xdg := filepath.Join(root, "home"), filepath.Join(root, "xdg")
		writeJSON(t, filepath.Join(home, ".pi", "web-search.json"), `{"perplexityApiKey":"pplx-from-legacy"}`)
		if err := os.MkdirAll(filepath.Join(xdg, "pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", xdg)
		available := PerplexityAvailable()
		before := cfgDirs()
		writeJSON(t, filepath.Join(xdg, "pi", "web-search.json"), `{"geminiApiKey":"gemini-created-later"}`)
		after := cfgDirs()
		want := dirs{filepath.Join(home, ".pi"), filepath.Join(home, ".pi", "web-search.json")}
		if before != want || after != want || !available {
			t.Fatalf("before=%+v after=%+v available=%v", before, after, available)
		}
	})

	tw(t, f, "web-search config path keeps XDG_CONFIG_HOME as the new-config target when both files are absent", func(t *testing.T) {
		root := pathEnv(t)
		xdg := filepath.Join(root, "xdg")
		if err := os.MkdirAll(filepath.Join(xdg, "pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", xdg)
		// Adapted: the target is $XDG_CONFIG_HOME/pig.
		want := dirs{filepath.Join(xdg, "pig"), filepath.Join(xdg, "pig", "web-search.json")}
		if got := cfgDirs(); got != want {
			t.Fatalf("%+v want %+v", got, want)
		}
	})

}

func mustRoot(t *testing.T) map[string]any {
	t.Helper()
	root, err := ReadConfigRoot()
	noErr(t, err)
	return root
}

func TestUpstream_ssrf_allow_ranges_config(t *testing.T) {
	const f = "ssrf-allow-ranges-config"
	load := func(t *testing.T, config *string) (SsrfConfig, error) {
		t.Helper()
		_, agent := isolate(t)
		ResetCaches()
		if config != nil {
			writeConfig(t, agent, *config)
		}
		return LoadSsrfConfig()
	}
	s := func(v string) *string { return &v }

	tw(t, f, "loadSsrfConfig defaults trustEnvProxy to false", func(t *testing.T) {
		got, err := load(t, s(`{"ssrf":{"allowRanges":["198.18.0.0/15"]}}`))
		noErr(t, err)
		if !reflect.DeepEqual(got, SsrfConfig{AllowRanges: []string{"198.18.0.0/15"}, TrustEnvProxy: false}) {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadFetchContentDomainPolicy reads normalized allow and deny hostnames", func(t *testing.T) {
		_, agent := isolate(t)
		ResetCaches()
		writeConfig(t, agent, `{"fetchContent":{"domainPolicy":{"allow":[" Example.COM. "],"deny":["blocked.example.com"]}}}`)
		got, err := LoadFetchContentDomainPolicy()
		noErr(t, err)
		if !reflect.DeepEqual(got, DomainPolicy{Allow: []string{"example.com"}, Deny: []string{"blocked.example.com"}}) {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadFetchContentDomainPolicy rejects malformed policy values", func(t *testing.T) {
		for _, policy := range []string{`{"allow":"example.com"}`, `{"deny":["*.example.com"]}`, `{"allow":["example.com",42]}`} {
			_, agent := isolate(t)
			ResetCaches()
			writeConfig(t, agent, `{"fetchContent":{"domainPolicy":`+policy+`}}`)
			_, err := LoadFetchContentDomainPolicy()
			wantErr(t, err, `fetchContent\.domainPolicy\.(allow|deny)`)
		}
	})

	tw(t, f, "loadSsrfConfig accepts an explicit trustEnvProxy opt-in", func(t *testing.T) {
		got, err := load(t, s(`{"ssrf":{"trustEnvProxy":true}}`))
		noErr(t, err)
		if !got.TrustEnvProxy || len(got.AllowRanges) != 0 {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadSsrfConfig rejects a non-boolean trustEnvProxy value", func(t *testing.T) {
		_, err := load(t, s(`{"ssrf":{"trustEnvProxy":"yes"}}`))
		wantErr(t, err, `ssrf\.trustEnvProxy in .* must be a boolean`)
	})

	tw(t, f, "loadSsrfAllowRanges throws when ssrf.allowRanges is not an array", func(t *testing.T) {
		for _, v := range []string{`"198.18.0.0/15"`, `{"198.18.0.0/15":true}`, `42`} {
			_, err := load(t, s(`{"ssrf":{"allowRanges":`+v+`}}`))
			wantErr(t, err, `ssrf\.allowRanges in .* must be an array of CIDR strings`)
		}
	})

	tw(t, f, "loadSsrfAllowRanges throws when an ssrf.allowRanges entry is not a string", func(t *testing.T) {
		_, err := load(t, s(`{"ssrf":{"allowRanges":["198.18.0.0/15",123]}}`))
		wantErr(t, err, `ssrf\.allowRanges in .* must contain only CIDR strings; entry 2 is number`)
	})

	tw(t, f, "loadSsrfAllowRanges returns trimmed, non-empty CIDR strings for a valid array", func(t *testing.T) {
		got, err := load(t, s(`{"ssrf":{"allowRanges":["198.18.0.0/15","  fd00::/8  ","","   "]}}`))
		noErr(t, err)
		if !reflect.DeepEqual(got.AllowRanges, []string{"198.18.0.0/15", "fd00::/8"}) {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadSsrfAllowRanges returns [] when the config file is missing", func(t *testing.T) {
		got, err := load(t, nil)
		noErr(t, err)
		if len(got.AllowRanges) != 0 {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadSsrfAllowRanges returns [] when ssrf.allowRanges is unset", func(t *testing.T) {
		got, err := load(t, s(`{"perplexityApiKey":"pplx-x"}`))
		noErr(t, err)
		if len(got.AllowRanges) != 0 {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "loadSsrfAllowRanges reports malformed config JSON", func(t *testing.T) {
		_, err := load(t, s(`{ not valid json`))
		wantErr(t, err, `Failed to parse .*web-search\.json`)
	})
}
