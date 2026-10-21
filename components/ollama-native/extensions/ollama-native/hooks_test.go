package ollamanative

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The hooks mirror Pi's types.ts ProviderRequestOptions.onPayload,
// StreamOptions.onResponse and StreamOptions.onProviderStreamEvent: the payload
// hook may replace the request, the response hook sees status and headers
// before the body is read, the stream-event hook sees every parsed event.

func TestOnPayloadSeesAndReplacesTheRequest(t *testing.T) {
	f := newFake(t)
	var seen map[string]any
	var seenModel map[string]any
	opts := sdk.ProviderStreamOptions{OnPayload: func(p any, model map[string]any) (any, error) {
		seen, seenModel = p.(map[string]any), model
		replaced := map[string]any{"model": "granite4.1:3b", "stream": true, "messages": []any{map[string]any{"role": "user", "content": "rewritten"}}, "options": map[string]any{"num_ctx": 4096}}
		return replaced, nil
	}}
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), opts)
	if s.terminal()["type"] != "done" {
		t.Fatalf("events = %v", s.types())
	}
	if seen["model"] != "granite4.1:3b" || seen["stream"] != true {
		t.Errorf("hook saw %v", seen)
	}
	if seenModel["id"] != "granite4.1:3b" {
		t.Errorf("hook model = %v", seenModel)
	}
	got := f.chats[0]
	msgs := got["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "rewritten" || got["options"].(map[string]any)["num_ctx"] != float64(4096) {
		t.Errorf("server got %v", got)
	}
}

func TestOnPayloadNilKeepsTheRequest(t *testing.T) {
	f := newFake(t)
	called := 0
	run(t, newClient(f.URL), graniteModel, userTranscript("keep me"), sdk.ProviderStreamOptions{OnPayload: func(any, map[string]any) (any, error) { called++; return nil, nil }})
	if called != 1 {
		t.Fatalf("hook called %d times", called)
	}
	if msgs := f.chats[0]["messages"].([]any); msgs[0].(map[string]any)["content"] != "keep me" {
		t.Errorf("server got %v", f.chats[0])
	}
}

func TestOnPayloadErrorEndsTheStreamBeforeSending(t *testing.T) {
	f := newFake(t)
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{OnPayload: func(any, map[string]any) (any, error) { return nil, errors.New("hook says no") }})
	end := s.terminal()
	if end["type"] != "error" || !strings.Contains(end["error"].(map[string]any)["errorMessage"].(string), "hook says no") {
		t.Fatalf("terminal = %v", end)
	}
	if len(f.chats) != 0 {
		t.Errorf("the request was sent: %v", f.chats)
	}
}

func TestOnResponseSeesStatusAndHeadersBeforeTheBody(t *testing.T) {
	f := newFake(t)
	inner := f.Server.Config.Handler
	f.Server.Config.Handler = withResponseHeader("X-Probe", "yes", inner)
	var order []string
	var resp map[string]any
	var model map[string]any
	opts := sdk.ProviderStreamOptions{
		OnResponse: func(r map[string]any, m map[string]any) error {
			order = append(order, "response")
			resp, model = r, m
			return nil
		},
		OnProviderStreamEvent: func(any, map[string]any) error {
			if len(order) == 1 {
				order = append(order, "event")
			}
			return nil
		},
	}
	run(t, newClient(f.URL), graniteModel, userTranscript("hi"), opts)
	if !reflect.DeepEqual(order, []string{"response", "event"}) {
		t.Fatalf("order = %v", order)
	}
	if resp["status"] != 200 {
		t.Errorf("status = %v", resp["status"])
	}
	headers, _ := resp["headers"].(map[string]string)
	if headers["x-probe"] != "yes" {
		t.Errorf("headers = %v", resp["headers"])
	}
	if model["id"] != "granite4.1:3b" {
		t.Errorf("model = %v", model)
	}
}

func TestOnResponseSeesAnErrorStatusToo(t *testing.T) {
	f := newFake(t)
	f.chat, f.chatStatus = "chat_404.json", 404
	var status any
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{OnResponse: func(r map[string]any, _ map[string]any) error { status = r["status"]; return nil }})
	if status != 404 || s.terminal()["type"] != "error" {
		t.Errorf("status = %v, terminal = %v", status, s.terminal()["type"])
	}
}

func TestOnResponseErrorEndsTheStream(t *testing.T) {
	f := newFake(t)
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{OnResponse: func(map[string]any, map[string]any) error { return errors.New("no thanks") }})
	end := s.terminal()
	if end["type"] != "error" || !strings.Contains(end["error"].(map[string]any)["errorMessage"].(string), "no thanks") {
		t.Fatalf("terminal = %v", end)
	}
}

func TestOnProviderStreamEventSeesEveryParsedChunk(t *testing.T) {
	f := newFake(t)
	var events []map[string]any
	var model map[string]any
	opts := sdk.ProviderStreamOptions{OnProviderStreamEvent: func(data any, m map[string]any) error {
		events = append(events, data.(map[string]any))
		model = m
		return nil
	}}
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), opts)
	if s.terminal()["type"] != "done" {
		t.Fatalf("events = %v", s.types())
	}
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(string(fixture(t, "chat_text.ndjson"))), "\n") {
		if l != "" {
			lines++
		}
	}
	if len(events) != lines {
		t.Fatalf("hook saw %d events, stream has %d lines", len(events), lines)
	}
	if events[0]["message"].(map[string]any)["content"] != "Hel" || events[len(events)-1]["done"] != true {
		t.Errorf("events = %v", events)
	}
	if model["id"] != "granite4.1:3b" {
		t.Errorf("model = %v", model)
	}
}

func TestOnProviderStreamEventErrorEndsTheStream(t *testing.T) {
	f := newFake(t)
	n := 0
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{OnProviderStreamEvent: func(any, map[string]any) error {
		n++
		if n == 2 {
			return errors.New("observer failed")
		}
		return nil
	}})
	end := s.terminal()
	if end["type"] != "error" || !strings.Contains(end["error"].(map[string]any)["errorMessage"].(string), "observer failed") {
		t.Fatalf("terminal = %v", end)
	}
}
