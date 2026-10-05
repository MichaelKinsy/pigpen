package typesafe_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// Cross-check against the official SDK. port/crosscheck/record_js.mjs runs every scenario in
// scenarios.json through @typesafe-ai/sdk 0.6.0 (built from the pinned commit) against a
// scripted local server and records the requests the server saw and the outcome. This test
// replays the same scenarios through the Go client against an equivalent scripted server
// and compares request shape (method, path, headers, body) and outcome (result or error
// class, message, status, request ID, body).

type step struct {
	Status   int               `json:"status"`
	Headers  map[string]string `json:"headers"`
	Body     json.RawMessage   `json:"body"`
	BodyText *string           `json:"bodyText"`
	DelayMs  int               `json:"delayMs"`
	Action   string            `json:"action"`
}

type scenario struct {
	Name   string `json:"name"`
	Note   string `json:"note"`
	Config struct {
		Retry         map[string]any    `json:"retry"`
		TimeoutMs     int               `json:"timeoutMs"`
		DefaultHdrs   map[string]string `json:"defaultHeaders"`
		BaseURLSuffix string            `json:"baseURLSuffix"`
	} `json:"config"`
	Call struct {
		Kind    string          `json:"kind"`
		Request json.RawMessage `json:"request"`
		Options struct {
			Headers   map[string]string `json:"headers"`
			Retry     map[string]any    `json:"retry"`
			TimeoutMs int               `json:"timeoutMs"`
		} `json:"options"`
	} `json:"call"`
	Responses []step `json:"responses"`
}

type seenRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type outcome struct {
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result"`
	Class     string          `json:"class"`
	Message   string          `json:"message"`
	Status    int             `json:"status"`
	RequestID *string         `json:"requestId"`
	Body      json.RawMessage `json:"body"`
}

type golden struct {
	Requests []seenRequest `json:"requests"`
	Outcome  outcome       `json:"outcome"`
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func retryOverrides(t *testing.T, m map[string]any) typesafe.RetryOverrides {
	t.Helper()
	var o typesafe.RetryOverrides
	ms := func(v any) *time.Duration { return typesafe.Ptr(time.Duration(v.(float64)) * time.Millisecond) }
	for k, v := range m {
		switch k {
		case "maxRetries":
			o.MaxRetries = typesafe.Ptr(int(v.(float64)))
		case "backoffInitialMs":
			o.BackoffInitial = ms(v)
		case "backoffMaxMs":
			o.BackoffMax = ms(v)
		case "backoffJitter":
			o.BackoffJitter = typesafe.Ptr(v.(float64))
		case "httpStatuses":
			o.HTTPStatuses = []int{}
			for _, s := range v.([]any) {
				o.HTTPStatuses = append(o.HTTPStatuses, int(s.(float64)))
			}
		case "apiConnectionError":
			o.APIConnectionError = typesafe.Ptr(v.(bool))
		case "apiTimeoutError":
			o.APITimeoutError = typesafe.Ptr(v.(bool))
		default:
			t.Fatalf("unmapped retry option %q", k)
		}
	}
	return o
}

func buildRequest(t *testing.T, raw json.RawMessage) typesafe.SystemOneRequest {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var req typesafe.SystemOneRequest
	if s, ok := fields["state"]; ok {
		if err := json.Unmarshal(s, &req.State); err != nil {
			t.Fatal(err)
		}
	}
	if q, ok := fields["questions"]; ok {
		qs, err := typesafe.ParseQuestions(q)
		if err != nil {
			// A client-side rejection of the wire form (for example a score map): reported as
			// the call's outcome, like the SDK's validation.
			t.Logf("ParseQuestions: %v", err)
			req.Questions = nil
			parseErr = err
		} else {
			req.Questions = qs
		}
	}
	if m, ok := fields["model"]; ok {
		_ = json.Unmarshal(m, &req.Model)
	}
	for k, v := range fields {
		if k == "state" || k == "questions" || k == "model" {
			continue
		}
		if req.Extra == nil {
			req.Extra = map[string]any{}
		}
		var x any
		if err := json.Unmarshal(v, &x); err != nil {
			t.Fatal(err)
		}
		req.Extra[k] = x
	}
	return req
}

// parseErr carries a ParseQuestions rejection to the outcome (single-goroutine use per scenario).
var parseErr error

// scriptedServer answers requests from steps, like record_js.mjs.
type scriptedServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

var keptHeader = func(n string) bool {
	return strings.HasPrefix(n, "x-") || n == "authorization" || n == "accept" || n == "content-type" || n == "content-length"
}

func (s *scriptedServer) snapshot() []seenRequest {
	s.srv.CloseClientConnections()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.seen...)
}

func newScripted(steps []step) *scriptedServer {
	s := &scriptedServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h := map[string]string{}
		for k, v := range r.Header {
			if n := strings.ToLower(k); keptHeader(n) {
				h[n] = strings.Join(v, ", ")
			}
		}
		if r.ContentLength >= 0 && len(body) > 0 {
			h["content-length"] = fmt.Sprint(len(body))
		}
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{Method: r.Method, Path: r.URL.RequestURI(), Headers: h, Body: string(body)})
		n := len(s.seen)
		s.mu.Unlock()
		st := step{Status: 599}
		text := "unscripted request"
		st.BodyText = &text
		if n <= len(steps) {
			st = steps[n-1]
		}
		if st.DelayMs > 0 {
			select {
			case <-time.After(time.Duration(st.DelayMs) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if st.Action == "destroy" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetLinger(0)
			}
			_ = conn.Close()
			return
		}
		for k, v := range st.Headers {
			w.Header().Set(k, v)
		}
		var out []byte
		switch {
		case st.BodyText != nil:
			out = []byte(*st.BodyText)
		case st.Body != nil:
			out = st.Body
			w.Header().Set("content-type", "application/json")
		}
		w.WriteHeader(st.Status)
		_, _ = w.Write(out)
	}))
	return s
}

func generic(t *testing.T, raw []byte) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %s", raw)
	}
	return v
}

func className(err error) string {
	t := reflect.TypeOf(err)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// knownDifferences are scenarios where the Go client deliberately differs; the test asserts
// that the difference exists (so a stale entry fails) and what it is.
var knownDifferences = map[string]string{
	"200-invalid-json":                     "the TS SDK returns the raw text as the result; the Go client decodes into typed answers and reports a *TypeSafeError",
	"200-empty-body":                       "the TS SDK returns undefined; the Go client returns a zero result",
	"unknown-answer-type-and-extra-usage":  "unknown fields inside a known answer, inside usage and at the top level of the result are dropped by the typed Go result; SystemOneWithResponse and SystemOneRaw keep the complete body",
	"score-map-criteria-client-side-error": "the wire form of a score map is rejected by ParseQuestions (a Go-only entry point) with a different message; Go's builders cannot express a map",
}

var runtimeHeader = regexp.MustCompile(`^go/\S+ \(\w+; \w+\)$`)

func TestCrossCheck_ClientMatchesTheOfficialSDK(t *testing.T) {
	var scenarios []scenario
	readJSON(t, "../../port/crosscheck/scenarios.json", &scenarios)
	var goldens map[string]golden
	readJSON(t, "../../port/crosscheck/golden.json", &goldens)
	if len(scenarios) != len(goldens) {
		t.Fatalf("%d scenarios but %d goldens: re-record with record_js.mjs", len(scenarios), len(goldens))
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			want, ok := goldens[sc.Name]
			if !ok {
				t.Fatal("no golden")
			}
			parseErr = nil
			srv := newScripted(sc.Responses)
			defer srv.srv.Close()
			cfg := typesafe.Config{
				APIKey: "test-key-not-real", BaseURL: srv.srv.URL + sc.Config.BaseURLSuffix, LogLevel: typesafe.LogOff,
				Retry: retryOverrides(t, sc.Config.Retry), Timeout: time.Duration(sc.Config.TimeoutMs) * time.Millisecond,
				DefaultHeaders: sc.Config.DefaultHdrs, Getenv: func(string) string { return "" },
			}
			client, err := typesafe.NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			opts := &typesafe.RequestOptions{
				Headers: sc.Call.Options.Headers, Retry: retryOverrides(t, sc.Call.Options.Retry),
				Timeout: time.Duration(sc.Call.Options.TimeoutMs) * time.Millisecond,
			}
			var got outcome
			ctx := t.Context()
			var res any
			var callErr error
			if sc.Call.Kind == "modelsList" {
				res, callErr = client.Models().List(ctx, opts)
			} else {
				req := buildRequest(t, sc.Call.Request)
				if parseErr != nil {
					callErr = parseErr
				} else {
					res, callErr = client.SystemOne(ctx, req, opts)
				}
			}
			if callErr == nil {
				raw, err := json.Marshal(res)
				if err != nil {
					t.Fatal(err)
				}
				got = outcome{OK: true, Result: raw}
			} else {
				got = outcome{Class: className(callErr), Message: callErr.Error()}
				var api *typesafe.APIError
				if errors.As(callErr, &api) {
					got.Status = api.Status
					id := api.RequestID
					if id != "" {
						got.RequestID = &id
					}
					if api.Body != nil {
						b, _ := json.Marshal(api.Body)
						got.Body = b
					}
				}
			}
			compareRequests(t, want.Requests, srv.snapshot())
			diffs := compareOutcome(t, want.Outcome, got)
			if reason, known := knownDifferences[sc.Name]; known {
				if len(diffs) == 0 {
					t.Fatalf("listed as a known difference (%s) but the outcomes are equal", reason)
				}
				t.Logf("known difference: %s: %v", reason, diffs)
			} else if len(diffs) > 0 {
				t.Errorf("outcome differs from the official SDK:\n  %s\n  official: %+v\n  go:       %+v", strings.Join(diffs, "\n  "), want.Outcome, got)
			}
		})
	}
}

func compareRequests(t *testing.T, want, got []seenRequest) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("request count: official %d, go %d", len(want), len(got))
		return
	}
	for i := range want {
		w, g := want[i], got[i]
		tag := fmt.Sprintf("request %d", i)
		if w.Method != g.Method || w.Path != g.Path {
			t.Errorf("%s: official %s %s, go %s %s", tag, w.Method, w.Path, g.Method, g.Path)
		}
		if !reflect.DeepEqual(generic(t, []byte(w.Body)), generic(t, []byte(g.Body))) {
			t.Errorf("%s body differs:\n official %s\n go       %s", tag, w.Body, g.Body)
		}
		if (w.Body == "") != (g.Body == "") {
			t.Errorf("%s: body presence differs", tag)
		}
		// Key order of the fixed fields: state, questions, model.
		if w.Body != "" && orderOfFixedKeys(w.Body) != orderOfFixedKeys(g.Body) {
			t.Errorf("%s: field order differs: official %s, go %s", tag, orderOfFixedKeys(w.Body), orderOfFixedKeys(g.Body))
		}
		names := map[string]bool{}
		for n := range w.Headers {
			names[n] = true
		}
		for n := range g.Headers {
			names[n] = true
		}
		var sorted []string
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			wv, wok := w.Headers[n]
			gv, gok := g.Headers[n]
			switch n {
			case "x-typesafe-sdk":
				if !gok || gv != "typesafe-sdk-go/0.6.0" {
					t.Errorf("%s: x-typesafe-sdk = %q, want typesafe-sdk-go/0.6.0 (official %q)", tag, gv, wv)
				}
			case "x-typesafe-runtime":
				if !gok || !runtimeHeader.MatchString(gv) {
					t.Errorf("%s: x-typesafe-runtime = %q, want go/<v> (<os>; <arch>) (official %q)", tag, gv, wv)
				}
			case "content-length":
				// encoding/json always escapes U+2028 and U+2029 (\u2028: 6 bytes instead of 3).
				n := strings.Count(w.Body, "\u2028") + strings.Count(w.Body, "\u2029")
				if wv2 := fmt.Sprint(len(w.Body) + 3*n); wv2 != gv {
					t.Errorf("%s: content-length official %q (expected %s with escapes), go %q", tag, wv, wv2, gv)
				}
			default:
				if wok != gok || wv != gv {
					t.Errorf("%s: header %s: official %q (present %v), go %q (present %v)", tag, n, wv, wok, gv, gok)
				}
			}
		}
	}
}

// orderOfFixedKeys returns the order of the top-level state/questions/model keys in a body.
func orderOfFixedKeys(body string) string {
	dec := json.NewDecoder(strings.NewReader(body))
	var order []string
	if _, err := dec.Token(); err != nil {
		return ""
	}
	for dec.More() {
		k, _ := dec.Token()
		key := k.(string)
		var skip json.RawMessage
		_ = dec.Decode(&skip)
		if key == "state" || key == "questions" || key == "model" {
			order = append(order, key)
		}
	}
	return strings.Join(order, ",")
}

func compareOutcome(t *testing.T, want, got outcome) []string {
	t.Helper()
	var diffs []string
	if want.OK != got.OK {
		return []string{fmt.Sprintf("ok: official %v, go %v", want.OK, got.OK)}
	}
	if want.OK {
		if !reflect.DeepEqual(generic(t, want.Result), generic(t, got.Result)) {
			diffs = append(diffs, fmt.Sprintf("result: official %s, go %s", want.Result, got.Result))
		}
		return diffs
	}
	if want.Class != got.Class {
		diffs = append(diffs, fmt.Sprintf("class: official %s, go %s", want.Class, got.Class))
	}
	wm, gm := want.Message, got.Message
	if want.Class == "APIConnectionError" { // the cause text belongs to the runtime's HTTP stack
		wm, gm = strings.SplitN(wm, ":", 2)[0], strings.SplitN(gm, ":", 2)[0]
	}
	if wm != gm {
		diffs = append(diffs, fmt.Sprintf("message: official %q, go %q", want.Message, got.Message))
	}
	if want.Status != got.Status {
		diffs = append(diffs, fmt.Sprintf("status: official %d, go %d", want.Status, got.Status))
	}
	if (want.RequestID == nil) != (got.RequestID == nil) || (want.RequestID != nil && *want.RequestID != *got.RequestID) {
		diffs = append(diffs, "request id differs")
	}
	if !reflect.DeepEqual(generic(t, want.Body), generic(t, got.Body)) {
		diffs = append(diffs, fmt.Sprintf("body: official %s, go %s", want.Body, got.Body))
	}
	return diffs
}
