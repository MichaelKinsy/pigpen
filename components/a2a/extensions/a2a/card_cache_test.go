package a2aext

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardWriter) WriteHeader(int)             {}

// The Agent Card is the same document for every request, so it is built and encoded once per advertised URL, not
// on every GET: it used to cost a card build, a JSON encoding and a handler for each request, 11 KB and 67 allocations.
func TestAgentCardIsEncodedOncePerAdvertisedURL(t *testing.T) {
	s, err := NewServer(serverConfig(), &scriptedWorker{}, serverEnv)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://x/.well-known/agent-card.json", nil)
	w := &discardWriter{h: http.Header{}}
	s.serveCard(w, r) // warm
	if allocs := testing.AllocsPerRun(50, func() { s.serveCard(w, r) }); allocs > 25 {
		t.Fatalf("serving the card allocated %.0f times per request; it must be served from a cached encoding", allocs)
	}
	first := httptest.NewRecorder()
	s.serveCard(first, r)
	second := httptest.NewRecorder()
	s.serveCard(second, r)
	if first.Body.String() != second.Body.String() || first.Body.Len() == 0 {
		t.Fatal("the card must be identical across requests")
	}
	// A different advertised URL (the listener's address changes when it starts) must not serve the old document.
	s.cfg.ExternalURL = "https://agent.example.com"
	third := httptest.NewRecorder()
	s.serveCard(third, r)
	if third.Body.String() == first.Body.String() {
		t.Fatal("the card must follow the advertised URL")
	}
}
