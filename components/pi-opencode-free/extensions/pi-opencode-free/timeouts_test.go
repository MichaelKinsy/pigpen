// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func errorMessageOf(t *testing.T, s streamed) string {
	t.Helper()
	if s.terminal()["type"] != "error" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
	return s.terminal()["error"].(map[string]any)["errorMessage"].(string)
}

func TestDefaultMaxRetriesIsTwo(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	message := errorMessageOf(t, collect(t, stream))
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 1 + 2 retries", got)
	}
	if !strings.Contains(message, "503") {
		t.Errorf("error = %q", message)
	}
}

func TestExplicitZeroMaxRetriesDisablesRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"maxRetries": float64(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	errorMessageOf(t, collect(t, stream))
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestDefaultRequestTimeoutBoundsResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		<-r.Context().Done()
	}))
	defer server.Close()
	c := &client{
		baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" },
		requestTimeout: 50 * time.Millisecond, // 50 ms stands in for the 10 minute default
	}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"maxRetries": float64(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message := errorMessageOf(t, collect(t, stream)); !strings.Contains(message, "timed out after 50 ms") {
		t.Errorf("error = %q", message)
	}
}

func TestHostTimeoutOverridesDefaultRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		<-r.Context().Done()
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"maxRetries": float64(0), "timeoutMs": float64(40)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message := errorMessageOf(t, collect(t, stream)); !strings.Contains(message, "timed out after 40 ms") {
		t.Errorf("error = %q", message)
	}
}

func TestStalledStreamEndsAfterIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := &client{
		baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" },
		streamIdle: 100 * time.Millisecond, // 100 ms stands in for the 5 minute default
	}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if message := errorMessageOf(t, collect(t, stream)); !strings.Contains(message, "stalled: no data for 100 ms") {
		t.Errorf("error = %q", message)
	}
}

func TestSteadyStreamIsNotCutByIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range 4 {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(60 * time.Millisecond):
			}
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	c := &client{
		baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" },
		streamIdle: 150 * time.Millisecond, // longer than each gap, shorter than the whole turn
	}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s := collect(t, stream); s.terminal()["type"] != "done" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
}
