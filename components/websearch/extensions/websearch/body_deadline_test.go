package websearch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A response whose body cannot be read after the headers arrived, over a real connection: the
// original's `response.json()` rejects, its catch block words every such failure as
// "<api> returned invalid JSON: <rejection>" (SearXNG keeps the abort in its activity entry),
// and the rejection is the signal's reason (the provider's deadline, the caller's abort) or
// "terminated" (the connection dropped). The oracle fixtures (provider-requests.json, "body-*"
// cases) prove the wording against the original for every provider; this proves that a real
// net/http body read produces the three failures the fixtures inject.
func TestBodyReadFailureOverHTTP(t *testing.T) {
	serve := func(t *testing.T, handler http.HandlerFunc) {
		_, agentDir := isolate(t)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		writeConfig(t, agentDir, fmt.Sprintf(`{"provider":"searxng","searxngBaseUrl":%q,"ssrf":{"allowRanges":["127.0.0.0/8"]}}`, srv.URL))
	}
	stall := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	t.Run("the provider's deadline", func(t *testing.T) {
		serve(t, stall)
		prev := providerTimeoutOverride
		providerTimeoutOverride = 100 * time.Millisecond
		t.Cleanup(func() { providerTimeoutOverride = prev })
		_, err := SearchWithSearXNG(bg(), "q", SearchOptions{})
		if err == nil || err.Error() != "SearXNG returned invalid JSON: The operation was aborted due to timeout" {
			t.Fatalf("%v", err)
		}
	})
	t.Run("the caller's abort", func(t *testing.T) {
		serve(t, stall)
		ctx, cancel := context.WithTimeout(bg(), 100*time.Millisecond)
		defer cancel()
		_, err := SearchWithSearXNG(ctx, "q", SearchOptions{})
		if err == nil || err.Error() != "SearXNG returned invalid JSON: This operation was aborted" {
			t.Fatalf("%v", err)
		}
	})
	t.Run("a connection that drops", func(t *testing.T) {
		serve(t, func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 500\r\n\r\n{\"results\":")
			_ = buf.Flush()
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
		})
		_, err := SearchWithSearXNG(bg(), "q", SearchOptions{})
		if err == nil || err.Error() != "SearXNG returned invalid JSON: terminated" {
			t.Fatalf("%v", err)
		}
	})
}
