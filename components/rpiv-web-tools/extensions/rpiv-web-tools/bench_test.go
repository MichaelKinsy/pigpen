package rpiv_web_tools

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// canned answers every request with one fixed response, so a benchmark measures the tool and not the fake network.
type canned struct{ contentType, body string }

func (c canned) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		io.Copy(io.Discard, req.Body)
	}
	h := http.Header{}
	h.Set("Content-Type", c.contentType)
	return &http.Response{StatusCode: 200, Status: "OK", Header: h, Body: io.NopCloser(strings.NewReader(c.body)), Request: req}, nil
}

// BenchmarkSearchTool is web_search on a Brave answer with two results; the cost includes reading the config file.
func BenchmarkSearchTool(b *testing.B) {
	os.Setenv(braveKeyEnv, "KEY")
	defer os.Unsetenv(braveKeyEnv)
	body := `{"web":{"results":[{"title":"T1","url":"https://one","description":"S1"},{"title":"T2","url":"https://two","description":"S2"}]}}`
	defer SetTransport(canned{"application/json", body})()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := searchTool(context.Background(), "go news", nil, nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFetchTool is web_fetch of a 20 KB HTML page through the built-in pipeline.
func BenchmarkFetchTool(b *testing.B) {
	page := "<html><head><title>Bench</title></head><body>" + strings.Repeat("<p>Hello &amp; welcome to <b>the</b> page</p>\n", 450) + "</body></html>"
	defer SetTransport(canned{"text/html", page})()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := fetchTool(context.Background(), "https://example.com/p", false, nil); err != nil {
			b.Fatal(err)
		}
	}
}
