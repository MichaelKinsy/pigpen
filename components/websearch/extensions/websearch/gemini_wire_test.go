package websearch

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The Gemini endpoints are fixed, so no pigeq scenario server can receive them. This test replays
// what the ORIGINAL sent and returned, recorded by port/record-gemini-requests.mjs from the
// unmodified oracle modules with a stubbed fetch (extensions/websearch/testdata/gemini-wire.json), and requires
// the port to send the same requests (method, URL, headers, body) in the same order and to return
// the same result.

type geminiWireCase struct {
	Env       map[string]string `json:"env"`
	Config    map[string]any    `json:"config"`
	ADC       map[string]any    `json:"adc"`
	Responses []wireResponse    `json:"responses"`
	Requests  []wireRequest     `json:"requests"`
	Result    any               `json:"result"`
	Error     *string           `json:"error"`
	Activity  []oracleActivity  `json:"activity"`
}

type wireResponse struct {
	Match   string            `json:"match"`
	Method  string            `json:"method"`
	Status  int               `json:"status"`
	JSON    json.RawMessage   `json:"json"`
	Text    string            `json:"text"`
	Headers map[string]string `json:"headers"`
	Throw   string            `json:"throw"`
}

type wireRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

// wireBody normalises a body for comparison: JSON as a value, a service account assertion as its
// decoded header and claims (iat as an offset, as the recorder does), anything else as text.
func wireBody(t *testing.T, body string) any {
	t.Helper()
	var v any
	if json.Unmarshal([]byte(body), &v) == nil {
		return v
	}
	form, err := url.ParseQuery(body)
	if err != nil || form.Get("assertion") == "" || strings.Count(form.Get("assertion"), ".") != 2 {
		return body
	}
	parts := strings.Split(form.Get("assertion"), ".")
	dec := func(s string) map[string]any {
		b, _ := base64.RawURLEncoding.DecodeString(s)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	claims := dec(parts[1])
	claims["exp"] = claims["exp"].(float64) - claims["iat"].(float64)
	delete(claims, "iat")
	return map[string]any{"grant_type": form.Get("grant_type"), "assertion": map[string]any{"header": dec(parts[0]), "claims": claims, "signed": parts[2] != ""}}
}

// recordedAssertion is the recorder's form of a service account body.
func recordedAssertion(t *testing.T, body string) any {
	t.Helper()
	form, _ := url.ParseQuery(body)
	if a := form.Get("assertion"); strings.HasPrefix(a, "{") {
		var m map[string]any
		_ = json.Unmarshal([]byte(a), &m)
		return map[string]any{"grant_type": form.Get("grant_type"), "assertion": m}
	}
	var v any
	if json.Unmarshal([]byte(body), &v) == nil {
		return v
	}
	return body
}

func TestGeminiWireFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/gemini-wire.json")
	noErr(t, err)
	var fx struct {
		Cases map[string]geminiWireCase `json:"cases"`
	}
	noErr(t, json.Unmarshal(raw, &fx))
	if len(fx.Cases) == 0 {
		t.Fatal("empty fixture")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	noErr(t, err)

	for name, c := range fx.Cases {
		t.Run(name, func(t *testing.T) {
			home, agentDir := geminiEnv(t)
			for k, v := range c.Env {
				t.Setenv(k, v)
			}
			if c.Config != nil {
				b, _ := json.Marshal(c.Config)
				writeConfig(t, agentDir, string(b))
			}
			if c.ADC != nil {
				adc := map[string]any{}
				for k, v := range c.ADC {
					adc[k] = v
				}
				if adc["private_key"] == "<generated>" {
					der, _ := x509.MarshalPKCS8PrivateKey(key)
					adc["private_key"] = "-----BEGIN PRIVATE KEY-----\n" + base64.StdEncoding.EncodeToString(der) + "\n-----END PRIVATE KEY-----\n"
				}
				b, _ := json.Marshal(adc)
				path := home + "/adc.json"
				noErr(t, os.WriteFile(path, b, 0o600))
				t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
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
			Activity.Clear()
			gen := c.Requests[0]
			for _, r := range c.Requests {
				if strings.Contains(r.URL, ":generateContent") {
					gen = r
				}
			}
			first := func() map[string]any { return jsonOf(t, *gen.Body) }
			text := func(part int) string {
				return first()["contents"].([]any)[0].(map[string]any)["parts"].([]any)[part].(map[string]any)["text"].(string)
			}
			switch {
			case strings.Contains(name, "search"):
				var r *AttributedSearchResponse
				if r, callErr = geminiSearch(bg(), text(0)); callErr == nil {
					result = toAny(t, r)
				}
			case strings.HasPrefix(name, "url-context"):
				var r *ExtractedContent
				if r, callErr = ExtractWithURLContext(bg(), strings.TrimPrefix(text(0)[strings.Index(text(0), "URL: "):], "URL: ")); callErr == nil && r != nil {
					result = toAny(t, r)
				}
			case name == "pdf-inline-conversion":
				pages := 5
				result, callErr = ExtractPDFViaGemini(bg(), []byte{1, 2, 3, 4}, GeminiPDFOptions{Pages: &pages, MaxPages: 2, Title: "Sample Document"})
			case strings.HasPrefix(name, "video-query"):
				model := strings.TrimSuffix(gen.URL[strings.LastIndex(gen.URL, "/models/")+len("/models/"):], ":generateContent")
				fd := first()["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["fileData"].(map[string]any)
				mime, _ := fd["mimeType"].(string)
				result, callErr = QueryGeminiAPIWithVideo(bg(), text(1), fd["fileUri"].(string), GeminiAPIOptions{Model: model, MimeType: mime, Timeout: 1e9})
			case strings.HasPrefix(name, "api-error"):
				_, callErr = QueryGeminiAPIWithVideo(bg(), text(1), "files/test", GeminiAPIOptions{Timeout: 1e9})
			default:
				t.Fatalf("no call mapped for %s", name)
			}

			requireActivity(t, name, c.Activity)
			if c.Error != nil {
				if callErr == nil || callErr.Error() != *c.Error {
					t.Fatalf("error %v, want %q", callErr, *c.Error)
				}
			} else {
				noErr(t, callErr)
				if !reflect.DeepEqual(result, c.Result) {
					t.Fatalf("result\n got %#v\nwant %#v", result, c.Result)
				}
			}
			if len(n.calls) != len(c.Requests) {
				t.Fatalf("%d requests, the original sent %d: %v", len(n.calls), len(c.Requests), n.urls())
			}
			for i, want := range c.Requests {
				got := n.calls[i]
				if got.Method != want.Method || got.URL != want.URL {
					t.Fatalf("request %d: %s %s, want %s %s", i, got.Method, got.URL, want.Method, want.URL)
				}
				headers := map[string]string{}
				for k := range got.Header {
					headers[strings.ToLower(k)] = got.Header.Get(k)
				}
				if !reflect.DeepEqual(headers, orEmpty(want.Headers)) {
					t.Fatalf("request %d headers %v, want %v", i, headers, want.Headers)
				}
				if want.Body == nil {
					if got.Body != "" {
						t.Fatalf("request %d body %q", i, got.Body)
					}
					continue
				}
				if !reflect.DeepEqual(wireBody(t, got.Body), recordedAssertion(t, *want.Body)) {
					t.Fatalf("request %d body\n got %s\nwant %s", i, got.Body, *want.Body)
				}
			}
		})
	}
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	noErr(t, json.Unmarshal([]byte(s), &m))
	return m
}

func toAny(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	noErr(t, err)
	var out any
	noErr(t, json.Unmarshal(b, &out))
	return out
}
