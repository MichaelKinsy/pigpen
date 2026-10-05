package warden

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeTypeSafe is a local stand-in for the TypeSafe API: no test in this package talks to the real service or
// reads a real key. It answers POST /v1/systemone with the answers a test scripts, and records every request.
type fakeTypeSafe struct {
	*httptest.Server
	mu       sync.Mutex
	requests []tsRequest
	// status, when not 200, makes every request fail with that status and a body that carries a marker a
	// leaked error message would show.
	status int
	// noul and scope script the answers by question id; anything unscripted answers 0.05 / expected_step.
	noul  map[string]float64
	scope string
}

type tsRequest struct {
	Auth string
	Path string
	Body map[string]any
}

const tsErrorMarker = "SECRET-UPSTREAM-DETAIL"

func newFakeTypeSafe(t *testing.T) *fakeTypeSafe {
	t.Helper()
	f := &fakeTypeSafe{noul: map[string]float64{}, scope: "expected_step", status: 200}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, tsRequest{Auth: r.Header.Get("Authorization"), Path: r.URL.Path, Body: body})
		status, noul, scope := f.status, map[string]float64{}, f.scope
		for k, v := range f.noul {
			noul[k] = v
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 200 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"` + tsErrorMarker + `"}`))
			return
		}
		answers := map[string]any{}
		qs, _ := body["questions"].(map[string]any)
		for id, q := range qs {
			switch q.(map[string]any)["type"] {
			case "noul":
				v, ok := noul[id]
				if !ok {
					v = 0.05
				}
				answers[id] = map[string]any{"type": "noul", "noul": v}
			case "choice":
				answers[id] = map[string]any{"type": "choice", "choice": scope, "confidence": 0.9, "probabilities": map[string]any{scope: 0.9}}
			case "score":
				answers[id] = map[string]any{"type": "score", "score": 1.0, "confidence": 0.9, "legend": map[string]any{}, "probabilities": map[string]any{}}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-fake", "answers": answers, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2}})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTypeSafe) set(noul map[string]float64) {
	f.mu.Lock()
	f.noul = noul
	f.mu.Unlock()
}

func (f *fakeTypeSafe) reqs() []tsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tsRequest(nil), f.requests...)
}

func (f *fakeTypeSafe) count() int { return len(f.reqs()) }
