package websearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Latency and allocations of one provider search against an in-process fake endpoint (the network is
// not measured). Run on a quiet CPU set, interleaved with the other build:
//
//	go test -run xxx -bench 'BenchmarkSearch' -benchmem -count 1

type benchDoer struct{ body string }

func (d benchDoer) Do(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(d.body)), Request: r}, nil
}

// benchEnv points the config at an empty directory and sets one environment variable.
func benchEnv(b *testing.B, key, value string) {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("PIG_CODING_AGENT_DIR", dir)
	b.Setenv("PI_CODING_AGENT_DIR", "")
	b.Setenv("XDG_CONFIG_HOME", "")
	b.Setenv(key, value)
	ResetCaches()
	b.Cleanup(ResetCaches)
}

const benchBraveBody = `{"web":{"results":[{"title":"One","url":"https://example.com/1","description":"first"},{"title":"Two","url":"https://example.com/2","description":"second"},{"title":"Three","url":"https://example.com/3","description":"third"}]}}`

// BenchmarkSearchBrave is the whole `Search` path (configuration, routing, request, parsing) of a
// provider that exists in every release: the comparison against the previous release.
func BenchmarkSearchBrave(b *testing.B) {
	benchEnv(b, "BRAVE_API_KEY", "bench-key")
	b.Cleanup(SetHTTP(benchDoer{benchBraveBody}))
	ctx := context.Background()
	o := FullSearchOptions{Provider: ProviderSelection{Name: "brave"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Search(ctx, "what shipped?", o); err != nil {
			b.Fatal(err)
		}
	}
}
