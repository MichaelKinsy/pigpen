package ollamanative

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func vals(kv map[string]any) sdk.ProviderStreamOptions { return sdk.ProviderStreamOptions{Values: kv} }

func requestOptions(t *testing.T, kv map[string]any) map[string]any {
	t.Helper()
	req := chatRequest(graniteModel, userTranscript("x"), vals(kv))
	o, _ := req["options"].(map[string]any)
	return o
}

func TestSamplingParamsReachOllamaOptions(t *testing.T) {
	o := requestOptions(t, map[string]any{
		"temperature": 0.7,
		"maxTokens":   float64(100),
		"samplingParams": map[string]any{
			"top_p": 0.9, "top_k": float64(40), "min_p": 0.05, "seed": float64(7),
			"repeat_penalty": 1.1, "stop": []any{"END"}, "num_ctx": float64(8192),
			"temperature": 0.1, // samplingParams override the named fields, as in Pi
		},
	})
	want := map[string]any{"temperature": 0.1, "num_predict": 100, "top_p": 0.9, "top_k": float64(40), "min_p": 0.05, "seed": float64(7),
		"repeat_penalty": 1.1, "stop": []any{"END"}, "num_ctx": float64(8192)}
	if !reflect.DeepEqual(o, want) {
		t.Errorf("options = %v", o)
	}
}

func TestThinkingLevels(t *testing.T) {
	qwen := map[string]any{"id": "qwen3:8b", "reasoning": true}
	gptoss := map[string]any{"id": "gpt-oss:20b", "reasoning": true}
	cases := []struct {
		name   string
		model  map[string]any
		values map[string]any
		want   any // nil: think absent
	}{
		{"level high on a bool model", qwen, map[string]any{"reasoning": "high"}, true},
		{"level minimal on a bool model", qwen, map[string]any{"reasoning": "minimal"}, true},
		{"off", qwen, map[string]any{"reasoning": "off"}, false},
		{"thinkingEnabled false", qwen, map[string]any{"thinkingEnabled": false}, false},
		{"thinkingEnabled beats the level", qwen, map[string]any{"thinkingEnabled": false, "reasoning": "high"}, false},
		{"gpt-oss minimal is low", gptoss, map[string]any{"reasoning": "minimal"}, "low"},
		{"gpt-oss low", gptoss, map[string]any{"reasoning": "low"}, "low"},
		{"gpt-oss medium", gptoss, map[string]any{"reasoning": "medium"}, "medium"},
		{"gpt-oss high", gptoss, map[string]any{"reasoning": "high"}, "high"},
		{"gpt-oss xhigh clamps to high", gptoss, map[string]any{"reasoning": "xhigh"}, "high"},
		{"gpt-oss max clamps to high", gptoss, map[string]any{"reasoning": "max"}, "high"},
		{"gpt-oss off still cannot be bool-off: Ollama takes false", gptoss, map[string]any{"reasoning": "off"}, false},
		{"reasoningEffort is a level too", gptoss, map[string]any{"reasoningEffort": "medium"}, "medium"},
		{"effort is a level too", gptoss, map[string]any{"effort": "low"}, "low"},
		{"level wins over effort", gptoss, map[string]any{"reasoning": "high", "effort": "low"}, "high"},
		{"nothing asked", qwen, map[string]any{}, nil},
		{"non-thinking model", graniteModel, map[string]any{"reasoning": "high"}, nil},
	}
	for _, c := range cases {
		got, has := chatRequest(c.model, userTranscript("x"), vals(c.values))["think"]
		if c.want == nil {
			if has {
				t.Errorf("%s: think = %v, want absent", c.name, got)
			}
			continue
		}
		if !has || got != c.want {
			t.Errorf("%s: think = %v (present %v), want %v", c.name, got, has, c.want)
		}
	}
}

func TestToolChoice(t *testing.T) {
	tr := map[string]any{"messages": []any{map[string]any{"role": "system", "content": "s", "toolsAdded": []any{map[string]any{"name": "read", "description": "d"}}}, map[string]any{"role": "user", "content": "x"}}}
	for _, choice := range []any{nil, "auto"} {
		kv := map[string]any{}
		if choice != nil {
			kv["toolChoice"] = choice
		}
		if chatRequest(graniteModel, tr, vals(kv))["tools"] == nil {
			t.Errorf("toolChoice %v: tools not offered", choice)
		}
	}
	if _, has := chatRequest(graniteModel, tr, vals(map[string]any{"toolChoice": "none"}))["tools"]; has {
		t.Error("toolChoice none still offered tools")
	}
}

func TestOptionsThatCannotBeHonoredAreRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"toolChoice \"any\"":           {"toolChoice": "any"},
		"a specific toolChoice":        {"toolChoice": map[string]any{"type": "tool", "name": "read"}},
		"deferred generation":          {"deferred": true},
		"deferred generation (window)": {"deferred": map[string]any{"window": "1h"}},
	}
	for label, kv := range cases {
		f := newFake(t)
		stream, err := newClient(f.URL).stream(graniteModel, userTranscript("x"), vals(kv))
		feature := strings.Split(label, " (")[0]
		want := "@pi-in-go/pigpen-ollama-native does not support " + feature + " yet. The request was not sent."
		if stream != nil || err == nil || err.Error() != want {
			t.Errorf("%s: stream=%v err=%v, want %q", label, stream, err, want)
		}
		if f.count() != 0 {
			t.Errorf("%s: a request was sent", label)
		}
	}
	// false is not a request for deferral.
	f := newFake(t)
	if s := run(t, newClient(f.URL), graniteModel, userTranscript("x"), vals(map[string]any{"deferred": false})); s.terminal()["type"] != "done" {
		t.Errorf("deferred false: %v", s.types())
	}
}

// Every key the host puts in Values (providerOptionsWire in PiG) has a stated
// disposition, so none is dropped by accident.
func TestEveryHostOptionKeyHasADisposition(t *testing.T) {
	keys := []string{"apiKey", "reasoning", "reasoningEffort", "effort", "cacheRetention", "sessionId", "transport", "maxTokens", "temperature",
		"timeoutMs", "websocketConnectTimeoutMs", "maxRetries", "maxRetryDelayMs", "thinkingEnabled", "thinkingBudgetTokens", "interleavedThinking",
		"thinking", "thinkingBudgets", "headers", "env", "samplingParams", "requestMetadata", "metadata", "toolChoice", "deferred", "hasOnPayload", "region", "profile"}
	for _, k := range keys {
		if d := optionDisposition[k]; d != "mapped" && d != "ignored" && d != "refused" {
			t.Errorf("option %q has disposition %q", k, d)
		}
	}
}

// Options Pi documents as hints a provider may ignore (sessionId, cacheRetention,
// transport, budgets, metadata, ...) must not change the request or fail it.
func TestPiHintsAreAcceptedAndDoNotChangeTheRequest(t *testing.T) {
	f := newFake(t)
	base := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), vals(map[string]any{}))
	_ = base
	hints := map[string]any{"sessionId": "s1", "cacheRetention": "long", "transport": "sse", "websocketConnectTimeoutMs": float64(5),
		"thinkingBudgets": map[string]any{"low": float64(1024)}, "thinkingBudgetTokens": float64(2048), "interleavedThinking": true,
		"metadata": map[string]any{"user_id": "u"}, "requestMetadata": map[string]any{"a": "b"}, "region": "us-east-1", "profile": "p", "hasOnPayload": true}
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), vals(hints))
	if s.terminal()["type"] != "done" {
		t.Fatalf("events = %v", s.types())
	}
	if !reflect.DeepEqual(f.chats[0], f.chats[1]) {
		t.Errorf("hints changed the request:\n%v\n%v", f.chats[0], f.chats[1])
	}
}

func capture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, int(n.Add(1)))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func okChat(t *testing.T) func(w http.ResponseWriter) {
	body := fixture(t, "chat_text.ndjson")
	return func(w http.ResponseWriter) { w.Write(body) }
}

func TestHeadersOption(t *testing.T) {
	var got http.Header
	ok := okChat(t)
	srv, _ := capture(t, func(w http.ResponseWriter, r *http.Request, _ int) { got = r.Header.Clone(); ok(w) })
	kv := map[string]any{"apiKey": "sk-a", "headers": map[string]any{"X-Trace": "t1", "Authorization": "Bearer other", "Content-Type": nil}}
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(kv))
	if s.terminal()["type"] != "done" {
		t.Fatalf("events = %v", s.types())
	}
	if got.Get("X-Trace") != "t1" {
		t.Errorf("X-Trace = %q", got.Get("X-Trace"))
	}
	if got.Get("Authorization") != "Bearer other" {
		t.Errorf("caller header must override the default: %q", got.Get("Authorization"))
	}
	if _, has := got["Content-Type"]; has {
		t.Errorf("a null header must suppress the default: %v", got["Content-Type"])
	}
}

func TestEnvOption(t *testing.T) {
	var auth string
	ok := okChat(t)
	srv, n := capture(t, func(w http.ResponseWriter, r *http.Request, _ int) { auth = r.Header.Get("Authorization"); ok(w) })
	c := &client{host: func() string { return "http://127.0.0.1:1" }, http: &http.Client{}, newID: func() string { return "id" }}
	kv := map[string]any{"env": map[string]any{"OLLAMA_HOST": srv.URL, "OLLAMA_API_KEY": "sk-env"}}
	s := run(t, c, graniteModel, userTranscript("hi"), vals(kv))
	if s.terminal()["type"] != "done" || n.Load() != 1 {
		t.Fatalf("events = %v, requests = %d", s.types(), n.Load())
	}
	if auth != "Bearer sk-env" {
		t.Errorf("Authorization = %q", auth)
	}
	// The explicit apiKey wins over the env key.
	kv["apiKey"] = "sk-key"
	run(t, c, graniteModel, userTranscript("hi"), vals(kv))
	if auth != "Bearer sk-key" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestMaxRetries(t *testing.T) {
	ok := okChat(t)
	srv, n := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		if i < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":"busy"}`, http.StatusTooManyRequests)
			return
		}
		ok(w)
	})
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"maxRetries": float64(2)}))
	if s.terminal()["type"] != "done" || n.Load() != 3 {
		t.Errorf("terminal = %v after %d requests", s.terminal()["type"], n.Load())
	}
	// Out of retries: the last answer is the error.
	srv2, n2 := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"error":"busy"}`, http.StatusServiceUnavailable)
	})
	s = run(t, newClient(srv2.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"maxRetries": float64(1)}))
	msg := s.terminal()["error"].(map[string]any)["errorMessage"].(string)
	if s.terminal()["type"] != "error" || n2.Load() != 2 || !strings.Contains(msg, "503") || !strings.Contains(msg, "busy") {
		t.Errorf("terminal = %v after %d requests: %s", s.terminal()["type"], n2.Load(), msg)
	}
}

func TestNoRetriesUnlessAsked(t *testing.T) {
	srv, n := capture(t, func(w http.ResponseWriter, r *http.Request, i int) { http.Error(w, `{"error":"busy"}`, 503) })
	run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{}))
	if n.Load() != 1 {
		t.Errorf("%d requests", n.Load())
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	srv, n := capture(t, func(w http.ResponseWriter, r *http.Request, i int) { http.Error(w, `{"error":"bad"}`, 400) })
	run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"maxRetries": float64(3)}))
	if n.Load() != 1 {
		t.Errorf("%d requests", n.Load())
	}
}

func TestMaxRetryDelayFailsFastWithTheRequestedDelay(t *testing.T) {
	srv, n := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, `{"error":"slow down"}`, 429)
	})
	start := time.Now()
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"maxRetries": float64(2), "maxRetryDelayMs": float64(1000)}))
	msg := s.terminal()["error"].(map[string]any)["errorMessage"].(string)
	if n.Load() != 1 || !strings.Contains(msg, "120") || time.Since(start) > 5*time.Second {
		t.Errorf("requests = %d, took %v, message %q", n.Load(), time.Since(start), msg)
	}
	// Default cap is 60 s: a 120 s request also fails at once.
	n.Store(0)
	run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"maxRetries": float64(2)}))
	if n.Load() != 1 {
		t.Errorf("default cap: %d requests", n.Load())
	}
}

func TestRetryWaitIsCancelledByTheSignal(t *testing.T) {
	srv, _ := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, `{"error":"later"}`, 429)
	})
	ctx, cancel := contextWithCancelAfter(100 * time.Millisecond)
	defer cancel()
	opts := sdk.ProviderStreamOptions{Signal: ctx, Values: map[string]any{"maxRetries": float64(2), "maxRetryDelayMs": float64(0)}}
	start := time.Now()
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), opts)
	if s.terminal()["reason"] != "aborted" || time.Since(start) > 5*time.Second {
		t.Errorf("terminal = %v after %v", s.terminal(), time.Since(start))
	}
}

func TestTimeoutMsBoundsTheWaitForTheResponse(t *testing.T) {
	srv, _ := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	})
	start := time.Now()
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"timeoutMs": float64(100)}))
	end := s.terminal()
	msg, _ := end["error"].(map[string]any)["errorMessage"].(string)
	if end["type"] != "error" || end["reason"] != "error" || !strings.Contains(msg, "timed out") || !strings.Contains(msg, "100") || time.Since(start) > 2*time.Second {
		t.Errorf("terminal = %v after %v", end, time.Since(start))
	}
}

func TestTimeoutMsDoesNotCutALongStream(t *testing.T) {
	body := fixture(t, "chat_text.ndjson")
	srv, _ := capture(t, func(w http.ResponseWriter, r *http.Request, i int) {
		lines := strings.SplitAfter(string(body), "\n")
		for _, l := range lines {
			fmt.Fprint(w, l)
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
	})
	s := run(t, newClient(srv.URL), graniteModel, userTranscript("hi"), vals(map[string]any{"timeoutMs": float64(200)}))
	if s.terminal()["type"] != "done" {
		t.Errorf("terminal = %v", s.terminal())
	}
}
