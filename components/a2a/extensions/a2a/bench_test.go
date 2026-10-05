package a2aext

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A2A request handling without the network: the server's http.Handler driven directly, a worker that answers at
// once. This is what the listener adds per request (authentication, version check, JSON-RPC, task bookkeeping).
// The listener is off unless configured; nothing here runs at startup.
//
//	go test -run xxx -bench . -benchmem
func benchServer(b *testing.B) *Server {
	b.Helper()
	s, err := NewServer(serverConfig(), &scriptedWorker{}, serverEnv)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s
}

func BenchmarkSendMessage(b *testing.B) {
	s := benchServer(b)
	h := s.Handler()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hello there"}]}}}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tokenA)
		r.Header.Set("A2A-Version", "1.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			b.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkAuthenticate(b *testing.B) {
	a, err := NewAuthenticator(serverConfig().Tokens, serverEnv, false)
	if err != nil {
		b.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://x/", nil)
	r.Header.Set("Authorization", "Bearer "+tokenB)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := a.Authenticate(r); !ok {
			b.Fatal("rejected")
		}
	}
}

func BenchmarkAgentCard(b *testing.B) {
	h := benchServer(b).Handler()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://x/.well-known/agent-card.json", nil))
		if w.Code != http.StatusOK {
			b.Fatalf("status %d", w.Code)
		}
	}
}
