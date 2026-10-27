// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type streamed struct {
	events []map[string]any
	result map[string]any
}

func (s streamed) types() []string {
	out := make([]string, 0, len(s.events))
	for _, event := range s.events {
		out = append(out, event["type"].(string))
	}
	return out
}

func (s streamed) terminal() map[string]any { return s.events[len(s.events)-1] }

func collect(t *testing.T, stream *sdk.ModelEventStream) streamed {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out streamed
	for event := range stream.Events(ctx) {
		out.events = append(out.events, event)
	}
	if ctx.Err() != nil {
		t.Fatalf("stream did not finish: %v (events %v)", ctx.Err(), out.types())
	}
	out.result = stream.Result()
	return out
}

func userTranscript(text string) map[string]any {
	return map[string]any{"messages": []any{map[string]any{"role": "user", "content": text, "timestamp": 1}}}
}

func freeModel(id string) map[string]any {
	return map[string]any{
		"id": id, "name": id, "api": "openai-completions", "provider": ProviderID,
		"reasoning": false, "input": []any{"text"},
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
