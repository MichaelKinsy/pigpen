package ollamanative

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fixture reads a recorded Ollama response from testdata. The files follow the
// wire format in Ollama's API documentation (docs/api.md): /api/tags and
// /api/show answer one JSON document, /api/chat streams newline-delimited JSON.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeOllama serves the recorded responses and counts every request it gets.
type fakeOllama struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
	chats    []map[string]any
	// chat is the fixture /api/chat streams, chatStatus its HTTP status.
	chat       string
	chatStatus int
	tags       string
	hold       chan struct{} // when set, /api/chat sends the first line then waits for it
}

func newFake(t *testing.T) *fakeOllama {
	t.Helper()
	f := &fakeOllama{chat: "chat_text.ndjson", tags: "tags.json", chatStatus: 200}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			w.Write(fixture(t, f.tags))
		case "/api/show":
			var in struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			name := strings.NewReplacer(":", "_", "/", "_").Replace(in.Model)
			b, err := os.ReadFile(filepath.Join("testdata", "show_"+name+".json"))
			if err != nil {
				http.Error(w, `{"error":"model not found"}`, 404)
				return
			}
			w.Write(b)
		case "/api/chat":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			f.chats = append(f.chats, in)
			f.mu.Unlock()
			w.WriteHeader(f.chatStatus)
			body := fixture(t, f.chat)
			if f.hold != nil {
				line, rest, _ := strings.Cut(string(body), "\n")
				io.WriteString(w, line+"\n")
				w.(http.Flusher).Flush()
				select {
				case <-f.hold:
				case <-r.Context().Done():
					return
				}
				body = []byte(rest)
			}
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeOllama) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

// newClient builds a client aimed at base with deterministic tool-call ids.
func newClient(base string) *client {
	n := 0
	return &client{host: func() string { return base }, http: &http.Client{}, newID: func() string { n++; return "call_t" + string(rune('0'+n)) }}
}

type streamed struct {
	events []map[string]any
	result map[string]any
}

func (s streamed) types() []string {
	var out []string
	for _, e := range s.events {
		out = append(out, e["type"].(string))
	}
	return out
}

func (s streamed) terminal() map[string]any { return s.events[len(s.events)-1] }

// collect drains a provider stream.
func collect(t *testing.T, stream *sdk.ModelEventStream) streamed {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var s streamed
	for e := range stream.Events(ctx) {
		s.events = append(s.events, e)
	}
	if ctx.Err() != nil {
		t.Fatalf("stream did not finish: %v (events %v)", ctx.Err(), s.types())
	}
	s.result = stream.Result()
	return s
}

func userTranscript(text string) map[string]any {
	return map[string]any{"messages": []any{map[string]any{"role": "user", "content": text, "timestamp": 1}}}
}

var graniteModel = map[string]any{"id": "granite4.1:3b", "name": "granite4.1:3b", "api": "ollama-native", "provider": "ollama-native", "reasoning": false}

func httpHandler(see func(map[string][]string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		see(r.Header)
		next.ServeHTTP(w, r)
	})
}
