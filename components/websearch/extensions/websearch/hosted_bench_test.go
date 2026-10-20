package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hosted providers on the inputs recorded in port/provider-wire.json, with the network replaced by
// canned responses. port/bench-provider-requests.mjs times the original on the same inputs.
//
//	go test -run xxx -bench HostedWire -benchmem -count 10
type cannedDoer struct {
	responses []string
	statuses  []int
	i         int
}

func (d *cannedDoer) Do(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}
	n := min(d.i, len(d.responses)-1)
	d.i++
	return &http.Response{StatusCode: d.statuses[n], Header: http.Header{}, Body: io.NopCloser(strings.NewReader(d.responses[n])), Request: r}, nil
}

func BenchmarkHostedWire(b *testing.B) {
	data, err := os.ReadFile(filepath.FromSlash("../../port/provider-wire.json"))
	if err != nil {
		b.Skip("port/provider-wire.json is not beside this module")
	}
	var cases []wireCase
	if err := json.Unmarshal(data, &cases); err != nil {
		b.Fatal(err)
	}
	for _, c := range cases {
		if c.Expect.Error != nil {
			continue
		}
		b.Run(c.Name, func(b *testing.B) {
			home := b.TempDir()
			b.Setenv("HOME", home)
			b.Setenv("PIG_HOME", home+"/pig")
			b.Setenv("PIG_CODING_AGENT_DIR", home+"/agent")
			for k, v := range c.Env {
				b.Setenv(k, v)
			}
			var first string
			_ = json.Unmarshal(c.Args[0], &first)
			var o wireOptions
			_ = json.Unmarshal(c.Args[len(c.Args)-1], &o)
			so := SearchOptions{NumResults: o.NumResults, RecencyFilter: o.RecencyFilter, DomainFilter: o.DomainFilter, IncludeContent: o.IncludeContent}
			eo := ExtractOptions{Prompt: o.Prompt, TimeoutMs: o.TimeoutMs, Lookup: pubLookup}
			d := &cannedDoer{}
			for _, r := range c.Responses {
				body, _ := r.Body.(string)
				if _, ok := r.Body.(string); !ok {
					j, _ := json.Marshal(r.Body)
					body = string(j)
				}
				d.responses = append(d.responses, body)
				d.statuses = append(d.statuses, r.Status)
			}
			defer SetHTTP(d)()
			defer SetPageFetch(func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
				req, _ := http.NewRequestWithContext(ctx, init.Method, u.String(), strings.NewReader(string(init.Body)))
				return d.Do(req)
			})()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.i = 0
				if _, err := wireCall(b, c.Fn, first, so, eo); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
