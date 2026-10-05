package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// twin marks a test as the twin of upstream cases (see twins_test.go): ids are
// "<file> | <full name>" from port/twins/typesafe-sdk-js.txt. Ported cases keep the
// original inputs and expectations, adapted only where the notes on the test say so.
func twin(t testing.TB, ids ...string) { t.Helper() }

// skipTwin records upstream cases with no Go counterpart, with the reason, and skips.
func skipTwin(t testing.TB, reason string, ids ...string) {
	t.Helper()
	t.Skip(reason)
}

// recordedRequest is what the fake transport saw.
type recordedRequest struct {
	URL    string
	Method string
	Header http.Header
	Raw    []byte
	Body   any // decoded JSON body, nil when there is none
	Ctx    context.Context
}

// mockDoer is the analog of the upstream mockFetch: it records requests and answers with
// whatever respond returns.
type mockDoer struct {
	mu       sync.Mutex
	requests []recordedRequest
	respond  func(r *recordedRequest) (*http.Response, error)
}

func newMock(respond func(r *recordedRequest) (*http.Response, error)) *mockDoer {
	return &mockDoer{respond: respond}
}

func (m *mockDoer) Do(req *http.Request) (*http.Response, error) {
	rec := recordedRequest{URL: req.URL.String(), Method: req.Method, Header: req.Header.Clone(), Ctx: req.Context()}
	if req.Body != nil {
		rec.Raw, _ = io.ReadAll(req.Body)
		if len(rec.Raw) > 0 {
			_ = json.Unmarshal(rec.Raw, &rec.Body)
		}
	}
	m.mu.Lock()
	m.requests = append(m.requests, rec)
	m.mu.Unlock()
	return m.respond(&rec)
}

func (m *mockDoer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *mockDoer) req(i int) recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[i]
}

// jsonResp builds a JSON response; headers are name, value pairs.
func jsonResp(status int, data any, headers ...string) *http.Response {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return textResp(status, string(raw), append([]string{"content-type", "application/json"}, headers...)...)
}

func textResp(status int, body string, headers ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: h, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func always(resp func() *http.Response) *mockDoer {
	return newMock(func(*recordedRequest) (*http.Response, error) { return resp(), nil })
}

// noEnv is an empty environment.
func noEnv(string) string { return "" }

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// newClient builds a client with no environment and jitter-free delays.
func newClient(t testing.TB, doer HTTPDoer, mutate ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{APIKey: "k", HTTPClient: doer, Getenv: noEnv}
	for _, m := range mutate {
		m(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.random = func() float64 { return 0 }
	return c
}

const modelsBody = `{"models":[{"name":"m","description":"d","release_date":"2026"}]}`

var modelCards = []ModelCard{{Name: "m", Description: "d", ReleaseDate: "2026"}}

var systemOneResponse = map[string]any{
	"model":   "m",
	"answers": map[string]any{"q1": map[string]any{"type": "noul", "noul": 0.5}},
	"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
}

// jsonEq compares got (any Go value, marshalled) with want (JSON text), ignoring key order.
func jsonEq(t testing.TB, got any, want string) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var g, w any
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("got is not JSON: %s", raw)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", raw, want)
	}
}

func decode(t testing.TB, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad JSON %s: %v", s, err)
	}
	return v
}

// recordingLogger records every call.
type logCall struct {
	Level   string
	Message string
	Args    []any
}

type recordingLogger struct {
	mu    sync.Mutex
	calls []logCall
}

func (l *recordingLogger) add(level, msg string, args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, logCall{level, msg, args})
}
func (l *recordingLogger) Debug(m string, a ...any) { l.add("debug", m, a) }
func (l *recordingLogger) Info(m string, a ...any)  { l.add("info", m, a) }
func (l *recordingLogger) Warn(m string, a ...any)  { l.add("warn", m, a) }
func (l *recordingLogger) Error(m string, a ...any) { l.add("error", m, a) }

func (l *recordingLogger) snapshot() []logCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logCall(nil), l.calls...)
}

func (l *recordingLogger) messages(level string) []string {
	var out []string
	for _, c := range l.snapshot() {
		if level == "" || c.Level == level {
			out = append(out, c.Message)
		}
	}
	return out
}

// hangingDoer waits for the request's context to end, like a hung fetch.
func hangingDoer() *mockDoer {
	return newMock(func(r *recordedRequest) (*http.Response, error) {
		<-r.Ctx.Done()
		return nil, r.Ctx.Err()
	})
}

// blockingBody is a response body that never yields until closed, whatever the context.
type blockingBody struct {
	mu     sync.Mutex
	closed chan struct{}
	closes int
}

func newBlockingBody() *blockingBody { return &blockingBody{closed: make(chan struct{})} }

func (b *blockingBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closes == 0 {
		close(b.closed)
	}
	b.closes++
	return nil
}

func (b *blockingBody) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closes
}

// errBody fails its first read with err.
type errBody struct{ err error }

func (b errBody) Read([]byte) (int, error) { return 0, b.err }
func (b errBody) Close() error             { return nil }

func respWithBody(status int, body io.ReadCloser, headers ...string) *http.Response {
	r := textResp(status, "", headers...)
	r.Body = body
	r.ContentLength = -1
	return r
}

func mustAs[T any](t testing.TB, err error) T {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("error %T (%v) is not %T", err, err, target)
	}
	return target
}

func notAs[T any](t testing.TB, err error) {
	t.Helper()
	var target T
	if errors.As(err, &target) {
		t.Fatalf("error %T (%v) unexpectedly is %T", err, err, target)
	}
}

func contains(t testing.TB, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func eq[T any](t testing.TB, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func noErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %T: %v", err, err)
	}
}

func ctxBG() context.Context { return context.Background() }

var _ = bytes.NewReader
var _ = time.Second

func fmtAny(v any) string { return fmt.Sprintf("%v %+v", v, v) }

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }

func asErr[T any](err error, target *T) bool { return errors.As(err, target) }
