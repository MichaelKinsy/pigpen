package a2aext

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
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

func benchSend(b *testing.B, cfg Config, w Worker, body []byte) {
	b.Helper()
	s, err := NewServer(cfg, w, serverEnv)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	h := s.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tokenA)
		r.Header.Set("A2A-Version", "1.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte(`"error"`)) {
			b.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}

var textTask = []byte(`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hello there"}]}}}`)

// streamedReply answers with 200 chunks, the shape of a long reply: every chunk is one task update.
func streamedReply() Worker {
	return &scriptedWorker{run: func(ctx context.Context, t Turn, up func(Update)) (Result, error) {
		for range 200 {
			up(Update{Text: "a chunk of the assistant's reply, 64 bytes long, as streamed.\n"})
		}
		return Result{}, nil
	}}
}

// The reply's cost grows with its length because every chunk is a task update; the task store is in that path.
func BenchmarkSendMessageStreamedReply(b *testing.B) {
	benchSend(b, serverConfig(), streamedReply(), textTask)
}

// The same with the task store on disk (a stateDir), the configuration of a listener that keeps its tasks.
func BenchmarkSendMessageStreamedReplyPersisted(b *testing.B) {
	cfg := serverConfig()
	cfg.StateDir = b.TempDir()
	benchSend(b, cfg, streamedReply(), textTask)
}

func BenchmarkSendMessagePersisted(b *testing.B) {
	cfg := serverConfig()
	cfg.StateDir = b.TempDir()
	benchSend(b, cfg, &scriptedWorker{}, textTask)
}

// A file and a data part: the cost of reading attachments into the prompt.
func BenchmarkSendMessageWithAttachments(b *testing.B) {
	file := bytes.Repeat([]byte("package main\n"), 300)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": map[string]any{"message": map[string]any{
		"messageId": "m1", "role": "ROLE_USER", "parts": []any{
			map[string]any{"text": "review"},
			map[string]any{"raw": file, "mediaType": "text/x-go", "filename": "main.go"},
			map[string]any{"data": map[string]any{"strict": true, "paths": []any{"a", "b"}}},
		}}}})
	benchSend(b, serverConfig(), &scriptedWorker{}, body)
}

func BenchmarkPromptOf(b *testing.B) {
	file := bytes.Repeat([]byte("package main\n"), 300)
	m := userMsg(a2a.NewTextPart("review"), rawPart(string(file), "text/x-go", "main.go"), a2a.NewDataPart(map[string]any{"strict": true}))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := promptOf(m); err != nil {
			b.Fatal(err)
		}
	}
}

// Cold start to registration: building the extension (flag, hooks, three tools, one command). No I/O.
func BenchmarkExtensionRegistration(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if Extension() == nil {
			b.Fatal("nil")
		}
	}
}
