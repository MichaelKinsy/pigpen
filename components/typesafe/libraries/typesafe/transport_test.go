package typesafe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// These tests use a real HTTP server: fakes do not reproduce the headers/body lifecycle.

func withServer(t *testing.T, h http.HandlerFunc, run func(baseURL string)) {
	t.Helper()
	s := httptest.NewServer(h)
	defer func() {
		s.CloseClientConnections()
		s.Close()
	}()
	run(s.URL)
}

// countingDoer wraps the real client and counts responses whose headers arrived.
type countingDoer struct {
	inner   *http.Client
	headers atomic.Int32
	after   func()
}

func (c *countingDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	if err == nil {
		c.headers.Add(1)
		if c.after != nil {
			c.after()
		}
	}
	return resp, err
}

func TestNativeTransport_SendsOneAuthorizationContentTypeAndOverridingCustomHeaderOnTheWire(t *testing.T) {
	twin(t, "native-transport.test.ts | native response transport sends one authorization, content type and overriding custom header on the wire")
	var observed http.Header
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
		_, _ = w.Write([]byte("{}"))
	}, func(baseURL string) {
		c, err := NewClient(Config{APIKey: "test", BaseURL: baseURL, Getenv: noEnv, DefaultHeaders: map[string]string{"authorization": "bad", "X-Team": "default", "content-type": "bad"}})
		noErr(t, err)
		_, err = c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Noul("?"))}}, &RequestOptions{Headers: map[string]string{"x-team": "call"}})
		noErr(t, err)
	})
	eq(t, observed.Values("Authorization"), []string{"Bearer test"})
	eq(t, observed.Values("X-Team"), []string{"call"})
	eq(t, observed.Values("Content-Type"), []string{"application/json"})
}

func TestNativeTransport_TimesOutAStalledBodyOnParsedWithResponseAndRawPaths(t *testing.T) {
	twin(t,
		"native-transport.test.ts | native response transport times out a stalled 200 body on parsed, withResponse and raw paths",
		"native-transport.test.ts | native response transport times out a stalled 503 body on parsed, withResponse and raw paths")
	for _, status := range []int{200, 503} {
		withServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, func(baseURL string) {
			doer := &countingDoer{inner: &http.Client{}}
			c, err := NewClient(Config{APIKey: "k", BaseURL: baseURL, Getenv: noEnv, Timeout: 200 * time.Millisecond, Retry: RetryOverrides{MaxRetries: Ptr(0)}, HTTPClient: doer})
			noErr(t, err)
			_, err = c.Models().List(ctxBG(), nil)
			mustAs[*APITimeoutError](t, err)
			_, err = c.Models().ListWithResponse(ctxBG(), nil)
			mustAs[*APITimeoutError](t, err)
			_, err = c.Models().ListRaw(ctxBG(), nil)
			mustAs[*APITimeoutError](t, err)
			eq(t, int(doer.headers.Load()), 3)
		})
	}
}

func TestNativeTransport_HonorsCallerCancellationAfterHeadersAndNeverRetries(t *testing.T) {
	twin(t,
		"native-transport.test.ts | native response transport honors caller cancellation after 200 headers and never retries",
		"native-transport.test.ts | native response transport honors caller cancellation after 503 headers and never retries")
	for _, status := range []int{200, 503} {
		var attempts atomic.Int32
		withServer(t, func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, func(baseURL string) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			doer := &countingDoer{inner: &http.Client{}, after: func() { time.AfterFunc(10*time.Millisecond, cancel) }}
			c, err := NewClient(Config{APIKey: "k", BaseURL: baseURL, Getenv: noEnv, HTTPClient: doer})
			noErr(t, err)
			_, err = c.Models().List(ctx, nil)
			mustAs[*APIUserAbortError](t, err)
			eq(t, int(attempts.Load()), 1)
		})
	}
}

func TestNativeTransport_RetriesABrokenBodyAndExposesTheSuccessfulResponseMetadata(t *testing.T) {
	twin(t,
		"native-transport.test.ts | native response transport retries a broken 200 body and exposes the successful response metadata",
		"native-transport.test.ts | native response transport retries a broken 503 body and exposes the successful response metadata")
	for _, status := range []int{200, 503} {
		var attempts atomic.Int32
		withServer(t, func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Typesafe-Request-Id", "req_"+string(rune('0'+n)))
			if n == 1 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("["))
				w.(http.Flusher).Flush()
				time.Sleep(10 * time.Millisecond)
				panic(http.ErrAbortHandler) // closes the connection mid-body
			}
			_, _ = w.Write([]byte(`{"models":[]}`))
		}, func(baseURL string) {
			c, err := NewClient(Config{APIKey: "k", BaseURL: baseURL, Getenv: noEnv, Retry: RetryOverrides{MaxRetries: Ptr(1), BackoffInitial: Ptr(time.Duration(0))}})
			noErr(t, err)
			res, err := c.Models().ListWithResponse(ctxBG(), nil)
			noErr(t, err)
			eq(t, res.Data, []ModelCard{})
			eq(t, int(attempts.Load()), 2)
			eq(t, res.RequestID, "req_2")
			eq(t, res.URL, baseURL+"/v1/models")
		})
	}
}

func TestNativeTransport_BuffersDelayedChunksBeforeRawHandoffAndPreservesAReadableBodyAfterCancellation(t *testing.T) {
	twin(t, "native-transport.test.ts | native response transport buffers delayed chunks before raw handoff and preserves a readable body after cancellation")
	var completed atomic.Bool
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("["))
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		completed.Store(true)
		_, _ = w.Write([]byte("]"))
	}, func(baseURL string) {
		ctx, cancel := context.WithCancel(context.Background())
		c, err := NewClient(Config{APIKey: "k", BaseURL: baseURL, Getenv: noEnv})
		noErr(t, err)
		raw, err := c.Models().ListRaw(ctx, nil)
		noErr(t, err)
		if !completed.Load() {
			t.Fatal("the raw response was handed over before the body finished")
		}
		eq(t, raw.URL, baseURL+"/v1/models")
		cancel()
		eq(t, string(raw.Body), "[]")
	})
}
