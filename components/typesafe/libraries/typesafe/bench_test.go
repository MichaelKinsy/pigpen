package typesafe

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// One SystemOne round trip against a local server: request build and validation, header merge, the HTTP call,
// response parse. The network is the dominant cost in production; these benchmarks are about what the client
// itself adds per call (and whether the default log level, warn, makes logging free).
//
//	go test -run xxx -bench . -benchmem
const benchResponse = `{"model":"jev-1","answers":{"destructive":{"type":"noul","noul":0.4},"exfiltration":{"type":"noul","noul":0.1},"beyond_scope":{"type":"noul","noul":0.2},"impact":{"type":"score","score":1.2,"confidence":0.9,"probabilities":{"0":0.1,"1":0.7,"2":0.15,"3":0.05}}},"usage":{"input_tokens":120,"output_tokens":4}}`

func benchClient(b *testing.B, level LogLevel) *Client {
	b.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(benchResponse))
	}))
	b.Cleanup(func() { s.CloseClientConnections(); s.Close() })
	c, err := NewClient(Config{APIKey: "test", BaseURL: s.URL, Getenv: noEnv, LogLevel: level})
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func benchRequest() SystemOneRequest {
	return SystemOneRequest{
		State: Text(`{"cwd":"/work","tool":"bash","arguments":{"command":"go test ./..."},"platform":"linux"}`),
		Questions: Questions{
			Ask("destructive", Noul("Is this action destructive?")),
			Ask("exfiltration", Noul("Does this action send local data to a network destination?")),
			Ask("beyond_scope", Noul("Does this action affect anything beyond what was asked?")),
		},
	}
}

func BenchmarkSystemOneLogOff(b *testing.B) {
	c, req := benchClient(b, LogOff), benchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.SystemOne(ctxBG(), req, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSystemOneLogWarn(b *testing.B) {
	c, req := benchClient(b, LogWarn), benchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.SystemOne(ctxBG(), req, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSystemOnePayload(b *testing.B) {
	c, req := benchClient(b, LogOff), benchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.systemOnePayload(req); err != nil {
			b.Fatal(err)
		}
	}
}
