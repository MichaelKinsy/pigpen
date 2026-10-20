package websearch

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The Datalab flow ends in an upload URL the server names, which a pigeq scenario route cannot
// point at itself, so this test replays what the ORIGINAL sent and returned. The requests were
// recorded by port/record-datalab-requests.mjs from the unmodified oracle modules with a stubbed
// fetch (testdata/datalab-wire.json); the port must send the same requests in the same order and
// return the same result or error.

type datalabWireRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type datalabWireCase struct {
	Env       map[string]string    `json:"env"`
	Config    map[string]any       `json:"config"`
	Responses []wireResponse       `json:"responses"`
	Requests  []datalabWireRequest `json:"requests"`
	Result    any                  `json:"result"`
	Error     *string              `json:"error"`
}

// datalabBody is the recorded form of a request body: text as text (JSON as a value), form data as
// its entries in order, a blob as its type and base64.
func datalabBody(c netCall) any {
	if c.Body == "" {
		return nil
	}
	if c.Method == "PUT" {
		return map[string]any{"blob": map[string]any{"type": c.Header.Get("Content-Type"), "base64": base64.StdEncoding.EncodeToString([]byte(c.Body))}}
	}
	var v any
	if json.Unmarshal([]byte(c.Body), &v) == nil {
		return v
	}
	return c.Body
}

// formEntries lists the multipart fields of a request in the order they were sent.
func formEntries(t *testing.T, c netCall) []any {
	t.Helper()
	fields := formFields(t, c)
	order := regexpFormOrder(c.Body)
	var out []any
	seen := map[string]int{}
	for _, name := range order {
		out = append(out, []any{name, fields[name][seen[name]]})
		seen[name]++
	}
	return out
}

func regexpFormOrder(body string) []string {
	var names []string
	for _, part := range strings.Split(body, "Content-Disposition: form-data; name=\"")[1:] {
		names = append(names, part[:strings.Index(part, "\"")])
	}
	return names
}

func TestDatalabWireFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/datalab-wire.json")
	noErr(t, err)
	var fx struct {
		Cases map[string]datalabWireCase `json:"cases"`
	}
	noErr(t, json.Unmarshal(raw, &fx))
	if len(fx.Cases) < 7 {
		t.Fatalf("%d cases", len(fx.Cases))
	}
	input := []byte{1, 2, 3, 4}
	for name, c := range fx.Cases {
		t.Run(name, func(t *testing.T) {
			_, agentDir := isolate(t)
			unsetenv(t, "DATALAB_API_KEY", "DATALAB_MODE", "DATALAB_PROCESSING_LOCATION", "DATALAB_API_BASE", "GEMINI_API_KEY")
			for k, v := range c.Env {
				t.Setenv(k, v)
			}
			if c.Config != nil {
				b, _ := json.Marshal(c.Config)
				writeConfig(t, agentDir, string(b))
			}
			n := useNet(t, func(call netCall) netReply {
				for _, r := range c.Responses {
					if !strings.Contains(call.URL, r.Match) || (r.Method != "" && r.Method != call.Method) {
						continue
					}
					if r.Throw != "" {
						return netReply{Err: errors.New(r.Throw)}
					}
					status := r.Status
					if status == 0 {
						status = 200
					}
					rep := netReply{Status: status, Body: r.Text}
					if r.JSON != nil {
						rep.Body = string(r.JSON)
					}
					if len(r.Headers) > 0 {
						rep.Header = http.Header{}
						for k, v := range r.Headers {
							rep.Header.Set(k, v)
						}
					}
					return rep
				}
				return netReply{Err: errors.New("Unexpected fetch: " + call.URL)}
			})

			var result any
			var callErr error
			switch name {
			case "default-flow":
				result, callErr = datalabResultJSON(ExtractPDFViaDatalab(bg(), input, DatalabPDFOptions{MaxPages: 5, Title: "Sample Document"}))
			case "eu-accurate-custom-base":
				result, callErr = datalabResultJSON(ExtractPDFViaDatalab(bg(), input, DatalabPDFOptions{MaxPages: 2, Title: "  Mixed CASE & Title -- with_symbols and a very very very very long name that goes past sixty chars "}))
			case "complete-on-convert", "http-error-redacted", "failed-conversion":
				result, callErr = datalabResultJSON(ExtractPDFViaDatalab(bg(), input, DatalabPDFOptions{MaxPages: 1, Title: "Doc"}))
			case "key-from-config":
				result, callErr = datalabResultJSON(ExtractPDFViaDatalab(bg(), input, DatalabPDFOptions{MaxPages: 3, Title: "Doc", Mode: "fast", ProcessingLocation: "eu"}))
			case "chain-from-pdf-extract":
				r, err := ExtractPDFToMarkdown(bg(), input, "https://example.test/docs/Q3_Results.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
				callErr = err
				if err == nil {
					result = map[string]any{"title": r.Title, "content": r.Content, "pages": float64(r.Pages), "chars": float64(r.Chars), "file": filepath.Base(r.OutputPath)}
				}
			default:
				t.Fatalf("no replay for case %s", name)
			}
			if _, ok := n.find(func(c netCall) bool { return c.Method == "PUT" }); ok {
				waitDeleted(t, n)
				time.Sleep(20 * time.Millisecond)
			}

			if c.Error != nil {
				if callErr == nil || callErr.Error() != *c.Error {
					t.Fatalf("error = %v, want %q", callErr, *c.Error)
				}
			} else {
				noErr(t, callErr)
				if !reflect.DeepEqual(result, c.Result) {
					t.Fatalf("result = %#v\nwant     %#v", result, c.Result)
				}
			}

			got := n.snapshot()
			if len(got) != len(c.Requests) {
				t.Fatalf("%d requests, want %d: %v", len(got), len(c.Requests), n.urls())
			}
			for i, want := range c.Requests {
				g := got[i]
				if g.Method != want.Method || g.URL != want.URL {
					t.Fatalf("request %d: %s %s, want %s %s", i, g.Method, g.URL, want.Method, want.URL)
				}
				var wantBody any
				_ = json.Unmarshal(want.Body, &wantBody)
				if text, ok := wantBody.(string); ok {
					var decoded any
					if json.Unmarshal([]byte(text), &decoded) == nil {
						wantBody = decoded
					}
				}
				isForm := false
				if m, ok := wantBody.(map[string]any); ok && m["form"] != nil {
					isForm = true
					wantBody = m["form"]
				}
				headers := map[string]string{}
				for k := range g.Header {
					headers[strings.ToLower(k)] = g.Header.Get(k)
				}
				if isForm {
					delete(headers, "content-type") // the boundary is the transport's
				}
				if !reflect.DeepEqual(headers, want.Headers) && !(len(headers) == 0 && len(want.Headers) == 0) {
					t.Fatalf("request %d headers = %v, want %v", i, headers, want.Headers)
				}
				var gotBody any
				if isForm {
					gotBody = formEntries(t, g)
				} else {
					gotBody = datalabBody(g)
				}
				if want.Body == nil || string(want.Body) == "null" {
					gotBody = nil
					if g.Body != "" {
						t.Fatalf("request %d has an unexpected body %q", i, g.Body)
					}
				}
				if !reflect.DeepEqual(normalizeWire(gotBody), normalizeWire(wantBody)) {
					t.Fatalf("request %d body = %#v, want %#v", i, gotBody, wantBody)
				}
			}
		})
	}
}

// normalizeWire round-trips through JSON so numbers and slices compare alike.
func normalizeWire(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func datalabResultJSON(r DatalabPDFResult, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	m := map[string]any{"markdown": r.Markdown, "pages": float64(r.Pages)}
	if r.ParseQualityScore != nil {
		m["parseQualityScore"] = *r.ParseQualityScore
	}
	return m, nil
}
