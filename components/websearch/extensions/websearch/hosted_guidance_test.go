package websearch

import (
	"context"
	"strings"
	"testing"
)

// The "key not found" guidance of each hosted provider, word for word as the originals write it
// (the getApiKey or requireApiKey of tinyfish.ts, search1api.ts, searchinfinity.ts, querit.ts, parallel.ts,
// ollama.ts, kagi.ts and brightdata.ts). The pigeq scenario web-search-hosted-missing-credentials compares the same
// texts against the original under Pi.
func TestHostedKeyGuidance(t *testing.T) {
	_, dir := isolate(t)
	path := dir + "/web-search.json"
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"tinyfish", func() error { _, err := SearchWithTinyFish(bg(), "q", SearchOptions{}); return err },
			"TinyFish API key not found. Either:\n  1. Create PATH with { \"tinyfishApiKey\": \"your-key\" }\n  2. Set TINYFISH_API_KEY environment variable\nGet a key at https://agent.tinyfish.ai/api-keys"},
		{"search1api", func() error { _, err := SearchWithSearch1API(bg(), "q", SearchOptions{}); return err },
			"Search1API key not found. Either:\n  1. Create PATH with { \"search1apiApiKey\": \"your-key\" }\n  2. Set SEARCH1API_KEY environment variable\nCreate a key at https://dashboard.search1api.com"},
		{"searchinfinity", func() error { _, err := SearchWithSearchinfinity(bg(), "q", SearchOptions{}); return err },
			"Searchinfinity API key not found. Either:\n  1. Create PATH with { \"searchinfinityApiKey\": \"your-key\" }\n  2. Set SEARCHINFINITY_API_KEY environment variable\nCreate a key at https://console.byteplus.com/search-infinity/api-key"},
		{"querit", func() error { _, err := SearchWithQuerit(bg(), "q", SearchOptions{}); return err },
			"Querit API key not found. Either:\n  1. Create PATH with { \"queritApiKey\": \"your-key\" }\n  2. Set QUERIT_API_KEY environment variable\nCreate a key at https://www.querit.ai/en/dashboard/api-keys"},
		{"parallel", func() error { _, err := SearchWithParallel(bg(), "q", SearchOptions{}); return err },
			"Parallel API key not found. Either:\n  1. Create PATH with { \"parallelApiKey\": \"your-key\" }\n  2. Set PARALLEL_API_KEY environment variable\nGet a key at https://platform.parallel.ai"},
		{"ollama", func() error { _, err := SearchWithOllama(bg(), "q", SearchOptions{}); return err },
			"Ollama API key not found. Either:\n  1. Create PATH with { \"ollamaApiKey\": \"your-key\" }\n  2. Set OLLAMA_API_KEY environment variable\nCreate a key at https://ollama.com/settings/keys"},
		{"kagi", func() error {
			_, err := ExtractWithKagi(context.Background(), "https://example.com/", pageOpts(), SsrfConfig{})
			return err
		}, "Kagi API key not found. Either:\n  1. Create PATH with { \"kagiApiKey\": \"your-key\" }\n  2. Set KAGI_API_KEY environment variable\nCreate a key at https://kagi.com/settings?p=api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			want := strings.ReplaceAll(c.want, "PATH", path)
			if err == nil || err.Error() != want {
				t.Fatalf("%v\nwant %s", err, want)
			}
		})
	}
	t.Run("brightdata", func(t *testing.T) {
		t.Setenv("BRIGHTDATA_SERP_ZONE", "pi_serp")
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		want := "Bright Data API key not found. Either:\n  1. Create " + path + " with { \"brightdataApiKey\": \"your-key\" }\n  2. Set BRIGHTDATA_API_KEY environment variable\nGet a key at https://brightdata.com/cp/setting/users"
		if err == nil || err.Error() != want {
			t.Fatalf("%v\nwant %s", err, want)
		}
	})
}
